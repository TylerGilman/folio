package store

import (
	"errors"
	"path/filepath"
	"testing"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "folio.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func mustBook(t *testing.T, s *Store, path string) Book {
	t.Helper()
	b, _, err := s.UpsertBook(path, path, 100)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestUpsertKeepsReadingState(t *testing.T) {
	s := openTest(t)
	b, added, err := s.UpsertBook("a.pdf", "A", 10)
	if err != nil || !added {
		t.Fatalf("first upsert: added=%v err=%v", added, err)
	}
	if err := s.SavePosition(b.ID, 5, 10); err != nil {
		t.Fatal(err)
	}
	again, added, err := s.UpsertBook("a.pdf", "Other title", 20)
	if err != nil || added {
		t.Fatalf("second upsert: added=%v err=%v", added, err)
	}
	if again.ID != b.ID || again.Title != "A" || again.LastPage != 5 || again.Size != 20 {
		t.Fatalf("got %+v", again)
	}
}

func TestSavePositionClampsAndComputesProgress(t *testing.T) {
	s := openTest(t)
	b := mustBook(t, s, "a.pdf")

	if err := s.SavePosition(b.ID, 50, 200); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetBook(b.ID)
	if got.LastPage != 50 || got.Pages != 200 || got.Progress != 0.25 || !got.Opened() {
		t.Fatalf("got %+v", got)
	}

	if err := s.SavePosition(b.ID, 999, 200); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetBook(b.ID)
	if got.LastPage != 200 || got.Progress != 1 {
		t.Fatalf("over-range page: got page %d progress %v", got.LastPage, got.Progress)
	}

	if err := s.SavePosition(b.ID, 1, 0); err == nil {
		t.Fatal("expected error for zero pages")
	}
	if err := s.SavePosition(12345, 1, 10); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown book: got %v", err)
	}
}

func TestListBooksOrdersRecentFirstAndFiltersByTag(t *testing.T) {
	s := openTest(t)
	a := mustBook(t, s, "a.pdf")
	b := mustBook(t, s, "b.pdf")
	mustBook(t, s, "c.pdf")
	if err := s.MarkOpened(b.ID); err != nil {
		t.Fatal(err)
	}

	books, err := s.ListBooks("")
	if err != nil {
		t.Fatal(err)
	}
	if len(books) != 3 || books[0].ID != b.ID {
		t.Fatalf("want recently opened first, got %v", titles(books))
	}

	if err := s.AddTag(a.ID, "  Linear   Algebra "); err != nil {
		t.Fatal(err)
	}
	if err := s.AddTag(a.ID, "linear algebra"); err != nil { // duplicate is fine
		t.Fatal(err)
	}
	if err := s.AddTag(b.ID, "math"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddTag(a.ID, "   "); err == nil {
		t.Fatal("expected error for blank tag")
	}

	shelf, err := s.ListBooks("linear algebra")
	if err != nil {
		t.Fatal(err)
	}
	if len(shelf) != 1 || shelf[0].ID != a.ID || len(shelf[0].Tags) != 1 || shelf[0].Tags[0] != "linear algebra" {
		t.Fatalf("got %+v", shelf)
	}

	tags, err := s.Tags()
	if err != nil {
		t.Fatal(err)
	}
	if len(tags) != 2 || tags[0] != (TagCount{"linear algebra", 1}) || tags[1] != (TagCount{"math", 1}) {
		t.Fatalf("got %+v", tags)
	}

	if err := s.RemoveTag(b.ID, "MATH"); err != nil {
		t.Fatal(err)
	}
	if shelf, _ := s.ListBooks("math"); len(shelf) != 0 {
		t.Fatalf("tag not removed: %v", titles(shelf))
	}
}

func TestMarkMissing(t *testing.T) {
	s := openTest(t)
	a := mustBook(t, s, "a.pdf")
	b := mustBook(t, s, "b.pdf")
	n, err := s.MarkMissing(map[string]bool{"a.pdf": true})
	if err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	ga, _ := s.GetBook(a.ID)
	gb, _ := s.GetBook(b.ID)
	if ga.Missing || !gb.Missing {
		t.Fatalf("a missing=%v b missing=%v", ga.Missing, gb.Missing)
	}
	// Finding the file again clears the flag.
	gb = mustBook(t, s, "b.pdf")
	if gb.Missing {
		t.Fatal("book still missing after upsert")
	}
}

func TestNotes(t *testing.T) {
	s := openTest(t)
	b := mustBook(t, s, "a.pdf")
	for _, n := range []struct {
		page int
		body string
	}{{9, "later"}, {2, "first"}, {9, "later still"}} {
		if _, err := s.AddNote(b.ID, n.page, n.body); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.AddNote(b.ID, 1, "  "); err == nil {
		t.Fatal("expected error for empty note")
	}
	if _, err := s.AddNote(b.ID, 0, "x"); err == nil {
		t.Fatal("expected error for page 0")
	}
	if _, err := s.AddNote(999, 1, "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown book: got %v", err)
	}

	notes, err := s.Notes(b.ID)
	if err != nil {
		t.Fatal(err)
	}
	var bodies []string
	for _, n := range notes {
		bodies = append(bodies, n.Body)
	}
	if len(bodies) != 3 || bodies[0] != "first" || bodies[1] != "later" || bodies[2] != "later still" {
		t.Fatalf("got %q", bodies)
	}

	bookID, err := s.DeleteNote(notes[0].ID)
	if err != nil || bookID != b.ID {
		t.Fatalf("delete: book=%d err=%v", bookID, err)
	}
	if _, err := s.DeleteNote(notes[0].ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete: got %v", err)
	}
	if notes, _ := s.Notes(b.ID); len(notes) != 2 {
		t.Fatalf("want 2 notes, got %d", len(notes))
	}
}

func TestDataPersistsAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "folio.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	b := mustBook(t, s, "a.pdf")
	if err := s.SavePosition(b.ID, 7, 10); err != nil {
		t.Fatal(err)
	}
	s.Close()

	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.GetBook(b.ID)
	if err != nil || got.LastPage != 7 {
		t.Fatalf("got %+v err=%v", got, err)
	}
}

func titles(books []Book) []string {
	var out []string
	for _, b := range books {
		out = append(out, b.Title)
	}
	return out
}
