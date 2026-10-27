// Package store keeps Folio metadata (books, reading position, tags, notes)
// in a local SQLite database. PDF files themselves stay on disk.
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// ErrNotFound is returned when a book or note does not exist.
var ErrNotFound = errors.New("not found")

// Book is one PDF in the library. Path is relative to the library root and
// always uses forward slashes.
type Book struct {
	ID         int64
	Path       string
	Title      string
	Size       int64
	Pages      int
	LastPage   int
	Progress   float64 // 0..1
	LastOpened time.Time
	AddedAt    time.Time
	Missing    bool
	Tags       []string
}

// Opened reports whether the book has ever been opened in the reader.
func (b Book) Opened() bool { return !b.LastOpened.IsZero() }

// Note is a page-linked note on a book.
type Note struct {
	ID        int64
	BookID    int64
	Page      int
	Body      string
	CreatedAt time.Time
}

// TagCount is a tag and the number of books that carry it.
type TagCount struct {
	Tag   string
	Count int
}

// Store wraps the SQLite database.
type Store struct {
	db  *sql.DB
	now func() time.Time
}

const schema = `
CREATE TABLE IF NOT EXISTS books (
	id          INTEGER PRIMARY KEY,
	path        TEXT NOT NULL UNIQUE,
	title       TEXT NOT NULL,
	size        INTEGER NOT NULL DEFAULT 0,
	pages       INTEGER NOT NULL DEFAULT 0,
	last_page   INTEGER NOT NULL DEFAULT 1,
	progress    REAL NOT NULL DEFAULT 0,
	last_opened INTEGER NOT NULL DEFAULT 0,
	added_at    INTEGER NOT NULL,
	missing     INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS book_tags (
	book_id INTEGER NOT NULL REFERENCES books(id) ON DELETE CASCADE,
	tag     TEXT NOT NULL,
	PRIMARY KEY (book_id, tag)
);
CREATE TABLE IF NOT EXISTS notes (
	id         INTEGER PRIMARY KEY,
	book_id    INTEGER NOT NULL REFERENCES books(id) ON DELETE CASCADE,
	page       INTEGER NOT NULL,
	body       TEXT NOT NULL,
	created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS notes_book ON notes(book_id, page);
`

// Open opens (and creates if needed) the database at path.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	dsn := "file:" + path + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// SQLite allows one writer; a single connection keeps things simple.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("init schema: %w", err)
	}
	return &Store{db: db, now: time.Now}, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// UpsertBook records a PDF found at path. A new book gets title; an existing
// one keeps its title and reading state, and is marked present again.
// It reports whether the book was newly added.
func (s *Store) UpsertBook(path, title string, size int64) (Book, bool, error) {
	b, err := s.bookByPath(path)
	switch {
	case err == nil:
		if _, err := s.db.Exec(`UPDATE books SET size = ?, missing = 0 WHERE id = ?`, size, b.ID); err != nil {
			return Book{}, false, err
		}
		b, err = s.GetBook(b.ID)
		return b, false, err
	case errors.Is(err, ErrNotFound):
		res, err := s.db.Exec(`INSERT INTO books (path, title, size, added_at) VALUES (?, ?, ?, ?)`,
			path, title, size, s.now().Unix())
		if err != nil {
			return Book{}, false, err
		}
		id, _ := res.LastInsertId()
		b, err = s.GetBook(id)
		return b, true, err
	default:
		return Book{}, false, err
	}
}

// MarkMissing flags every book whose path is not in present as missing and
// returns how many books are missing afterwards.
func (s *Store) MarkMissing(present map[string]bool) (int, error) {
	rows, err := s.db.Query(`SELECT id, path FROM books`)
	if err != nil {
		return 0, err
	}
	var gone []int64
	for rows.Next() {
		var id int64
		var p string
		if err := rows.Scan(&id, &p); err != nil {
			rows.Close()
			return 0, err
		}
		if !present[p] {
			gone = append(gone, id)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for _, id := range gone {
		if _, err := s.db.Exec(`UPDATE books SET missing = 1 WHERE id = ?`, id); err != nil {
			return 0, err
		}
	}
	return len(gone), nil
}

const bookCols = `id, path, title, size, pages, last_page, progress, last_opened, added_at, missing`

func scanBook(sc interface{ Scan(...any) error }) (Book, error) {
	var b Book
	var lastOpened, added int64
	var missing int
	err := sc.Scan(&b.ID, &b.Path, &b.Title, &b.Size, &b.Pages, &b.LastPage, &b.Progress, &lastOpened, &added, &missing)
	if err != nil {
		return Book{}, err
	}
	if lastOpened > 0 {
		b.LastOpened = time.Unix(lastOpened, 0)
	}
	b.AddedAt = time.Unix(added, 0)
	b.Missing = missing != 0
	return b, nil
}

func (s *Store) bookByPath(path string) (Book, error) {
	b, err := scanBook(s.db.QueryRow(`SELECT `+bookCols+` FROM books WHERE path = ?`, path))
	if errors.Is(err, sql.ErrNoRows) {
		return Book{}, ErrNotFound
	}
	return b, err
}

// GetBook returns a book with its tags.
func (s *Store) GetBook(id int64) (Book, error) {
	b, err := scanBook(s.db.QueryRow(`SELECT `+bookCols+` FROM books WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Book{}, ErrNotFound
	}
	if err != nil {
		return Book{}, err
	}
	tags, err := s.tagsFor([]int64{id})
	if err != nil {
		return Book{}, err
	}
	b.Tags = tags[id]
	return b, nil
}

// ListBooks returns books, most recently opened first, then by title.
// A non-empty tag limits the list to books with that tag.
func (s *Store) ListBooks(tag string) ([]Book, error) {
	q := `SELECT ` + bookCols + ` FROM books`
	var args []any
	if tag != "" {
		q += ` WHERE id IN (SELECT book_id FROM book_tags WHERE tag = ?)`
		args = append(args, tag)
	}
	q += ` ORDER BY last_opened DESC, title COLLATE NOCASE`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	var books []Book
	var ids []int64
	for rows.Next() {
		b, err := scanBook(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		books = append(books, b)
		ids = append(ids, b.ID)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	tags, err := s.tagsFor(ids)
	if err != nil {
		return nil, err
	}
	for i := range books {
		books[i].Tags = tags[books[i].ID]
	}
	return books, nil
}

func (s *Store) tagsFor(ids []int64) (map[int64][]string, error) {
	out := map[int64][]string{}
	if len(ids) == 0 {
		return out, nil
	}
	want := map[int64]bool{}
	for _, id := range ids {
		want[id] = true
	}
	rows, err := s.db.Query(`SELECT book_id, tag FROM book_tags ORDER BY tag COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var t string
		if err := rows.Scan(&id, &t); err != nil {
			return nil, err
		}
		if want[id] {
			out[id] = append(out[id], t)
		}
	}
	return out, rows.Err()
}

// MarkOpened sets the book's last-opened time to now.
func (s *Store) MarkOpened(id int64) error {
	return s.exec1(`UPDATE books SET last_opened = ? WHERE id = ?`, s.now().Unix(), id)
}

// SavePosition stores the current page and page count, and derives progress.
// Page is clamped to 1..pages.
func (s *Store) SavePosition(id int64, page, pages int) error {
	if pages < 1 {
		return fmt.Errorf("pages must be positive")
	}
	page = max(1, min(page, pages))
	progress := float64(page) / float64(pages)
	return s.exec1(`UPDATE books SET last_page = ?, pages = ?, progress = ?, last_opened = ? WHERE id = ?`,
		page, pages, progress, s.now().Unix(), id)
}

// NormalizeTag trims a tag, collapses inner whitespace and lowercases it.
func NormalizeTag(tag string) string {
	return strings.ToLower(strings.Join(strings.Fields(tag), " "))
}

// AddTag attaches tag to a book. Empty tags are rejected.
func (s *Store) AddTag(id int64, tag string) error {
	tag = NormalizeTag(tag)
	if tag == "" {
		return fmt.Errorf("empty tag")
	}
	if _, err := s.GetBook(id); err != nil {
		return err
	}
	_, err := s.db.Exec(`INSERT OR IGNORE INTO book_tags (book_id, tag) VALUES (?, ?)`, id, tag)
	return err
}

// RemoveTag detaches tag from a book.
func (s *Store) RemoveTag(id int64, tag string) error {
	_, err := s.db.Exec(`DELETE FROM book_tags WHERE book_id = ? AND tag = ?`, id, NormalizeTag(tag))
	return err
}

// Tags lists all tags with book counts, alphabetically.
func (s *Store) Tags() ([]TagCount, error) {
	rows, err := s.db.Query(`SELECT tag, COUNT(*) FROM book_tags GROUP BY tag`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TagCount
	for rows.Next() {
		var tc TagCount
		if err := rows.Scan(&tc.Tag, &tc.Count); err != nil {
			return nil, err
		}
		out = append(out, tc)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Tag < out[j].Tag })
	return out, rows.Err()
}

// AddNote adds a note to a book at page.
func (s *Store) AddNote(bookID int64, page int, body string) (Note, error) {
	body = strings.TrimSpace(body)
	if body == "" {
		return Note{}, fmt.Errorf("empty note")
	}
	if page < 1 {
		return Note{}, fmt.Errorf("page must be positive")
	}
	if _, err := s.GetBook(bookID); err != nil {
		return Note{}, err
	}
	now := s.now()
	res, err := s.db.Exec(`INSERT INTO notes (book_id, page, body, created_at) VALUES (?, ?, ?, ?)`,
		bookID, page, body, now.Unix())
	if err != nil {
		return Note{}, err
	}
	id, _ := res.LastInsertId()
	return Note{ID: id, BookID: bookID, Page: page, Body: body, CreatedAt: time.Unix(now.Unix(), 0)}, nil
}

// DeleteNote removes a note and returns the book it belonged to.
func (s *Store) DeleteNote(id int64) (int64, error) {
	var bookID int64
	err := s.db.QueryRow(`SELECT book_id FROM notes WHERE id = ?`, id).Scan(&bookID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, err
	}
	_, err = s.db.Exec(`DELETE FROM notes WHERE id = ?`, id)
	return bookID, err
}

// Notes lists a book's notes ordered by page, then creation.
func (s *Store) Notes(bookID int64) ([]Note, error) {
	rows, err := s.db.Query(`SELECT id, book_id, page, body, created_at FROM notes
		WHERE book_id = ? ORDER BY page, created_at, id`, bookID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Note
	for rows.Next() {
		var n Note
		var created int64
		if err := rows.Scan(&n.ID, &n.BookID, &n.Page, &n.Body, &created); err != nil {
			return nil, err
		}
		n.CreatedAt = time.Unix(created, 0)
		out = append(out, n)
	}
	return out, rows.Err()
}

func (s *Store) exec1(q string, args ...any) error {
	res, err := s.db.Exec(q, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
