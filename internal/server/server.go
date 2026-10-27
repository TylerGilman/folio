// Package server is Folio's HTTP layer: HTML pages, Datastar SSE endpoints,
// PDF file serving and Markdown export.
package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/starfederation/datastar-go/datastar"

	"github.com/TylerGilman/folio/internal/library"
	"github.com/TylerGilman/folio/internal/store"
	"github.com/TylerGilman/folio/web"
)

// DefaultMaxUpload is the default upload size limit per request.
const DefaultMaxUpload = 1 << 30 // 1 GiB

// Server serves the Folio UI and API.
type Server struct {
	lib       *library.Library
	tmpl      *template.Template
	mux       *http.ServeMux
	maxUpload int64
}

// New builds a server for lib.
func New(lib *library.Library) (*Server, error) {
	tmpl, err := template.New("").Funcs(funcs).ParseFS(web.Templates, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("parse templates: %w", err)
	}
	static, err := fs.Sub(web.Static, "static")
	if err != nil {
		return nil, err
	}
	s := &Server{lib: lib, tmpl: tmpl, mux: http.NewServeMux(), maxUpload: DefaultMaxUpload}

	s.mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(static)))
	s.mux.HandleFunc("GET /{$}", s.handleLibrary)
	s.mux.HandleFunc("POST /scan", s.handleScan)
	s.mux.HandleFunc("POST /upload", s.handleUpload)
	s.mux.HandleFunc("POST /books/{id}/tags", s.handleAddTag)
	s.mux.HandleFunc("DELETE /books/{id}/tags/{tag}", s.handleRemoveTag)
	s.mux.HandleFunc("GET /read/{id}", s.handleReader)
	s.mux.HandleFunc("GET /files/{id}", s.handleFile)
	s.mux.HandleFunc("POST /api/books/{id}/position", s.handlePosition)
	s.mux.HandleFunc("POST /books/{id}/notes", s.handleAddNote)
	s.mux.HandleFunc("DELETE /notes/{id}", s.handleDeleteNote)
	s.mux.HandleFunc("GET /books/{id}/notes.md", s.handleBookMarkdown)
	s.mux.HandleFunc("GET /notes.md", s.handleAllMarkdown)
	return s, nil
}

// SetMaxUpload changes the upload size limit in bytes.
func (s *Server) SetMaxUpload(n int64) { s.maxUpload = n }

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.mux.ServeHTTP(w, r) }

var funcs = template.FuncMap{
	"pct":     func(f float64) int { return int(f*100 + 0.5) },
	"ago":     humanAgo,
	"lower":   strings.ToLower,
	"join":    strings.Join,
	"pathesc": url.PathEscape,
	"size":    humanSize,
}

// --- library -------------------------------------------------------------

type libraryView struct {
	Filter string
	Books  []cardView
	Tags   []store.TagCount
	Total  int
	Flash  string
}

type cardView struct {
	store.Book
	Filter string
}

func (s *Server) libraryView(filter, flash string) (libraryView, error) {
	filter = store.NormalizeTag(filter)
	books, err := s.lib.Store.ListBooks(filter)
	if err != nil {
		return libraryView{}, err
	}
	tags, err := s.lib.Store.Tags()
	if err != nil {
		return libraryView{}, err
	}
	total := len(books)
	if filter != "" {
		all, err := s.lib.Store.ListBooks("")
		if err != nil {
			return libraryView{}, err
		}
		total = len(all)
	}
	v := libraryView{Filter: filter, Tags: tags, Total: total, Flash: flash}
	for _, b := range books {
		v.Books = append(v.Books, cardView{Book: b, Filter: filter})
	}
	return v, nil
}

func (s *Server) handleLibrary(w http.ResponseWriter, r *http.Request) {
	v, err := s.libraryView(r.URL.Query().Get("tag"), r.URL.Query().Get("msg"))
	if err != nil {
		serverError(w, err)
		return
	}
	s.render(w, "library-page", v)
}

// patchLibrary re-renders the library region over SSE. It returns the SSE
// stream for follow-up events, or nil if it already wrote an error.
func (s *Server) patchLibrary(w http.ResponseWriter, r *http.Request, flash string) *datastar.ServerSentEventGenerator {
	v, err := s.libraryView(r.URL.Query().Get("filter"), flash)
	if err != nil {
		serverError(w, err)
		return nil
	}
	html, err := s.renderString("library", v)
	if err != nil {
		serverError(w, err)
		return nil
	}
	sse := datastar.NewSSE(w, r)
	if err := sse.PatchElements(html); err != nil {
		log.Printf("patch library: %v", err)
	}
	return sse
}

func (s *Server) handleScan(w http.ResponseWriter, r *http.Request) {
	res, err := s.lib.Scan()
	flash := fmt.Sprintf("Scan done: %d PDFs, %d new, %d missing.", res.Found, res.Added, res.Missing)
	if err != nil {
		log.Printf("scan: %v", err)
		flash = "Scan failed: " + err.Error()
	}
	s.patchLibrary(w, r, flash)
}

func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, s.maxUpload)
	mr, err := r.MultipartReader()
	if err != nil {
		http.Error(w, "expected a multipart upload", http.StatusBadRequest)
		return
	}
	var added, rejected []string
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			redirectMsg(w, r, "Upload failed: "+err.Error())
			return
		}
		if part.FormName() != "files" || part.FileName() == "" {
			part.Close()
			continue
		}
		b, err := s.lib.Import(part.FileName(), part)
		part.Close()
		switch {
		case errors.Is(err, library.ErrNotPDF):
			rejected = append(rejected, part.FileName())
		case err != nil:
			redirectMsg(w, r, "Upload failed: "+err.Error())
			return
		default:
			added = append(added, b.Title)
		}
	}
	msg := fmt.Sprintf("Added %d PDF(s).", len(added))
	if len(rejected) > 0 {
		msg += " Skipped (not a PDF): " + strings.Join(rejected, ", ")
	}
	redirectMsg(w, r, msg)
}

func (s *Server) handleAddTag(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	flash := ""
	// Accept "a, b" to add several tags at once.
	for _, t := range strings.Split(r.FormValue("tag"), ",") {
		if store.NormalizeTag(t) == "" {
			continue
		}
		if err := s.lib.Store.AddTag(id, t); err != nil {
			flash = "Could not add tag: " + err.Error()
		}
	}
	sse := s.patchLibrary(w, r, flash)
	if sse != nil {
		// Morphing keeps a focused input's typed value; clear it explicitly.
		sse.ExecuteScript(fmt.Sprintf(`document.querySelector('#book-%d .add-tag input').value = ''`, id))
	}
}

func (s *Server) handleRemoveTag(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	flash := ""
	if err := s.lib.Store.RemoveTag(id, r.PathValue("tag")); err != nil {
		flash = "Could not remove tag: " + err.Error()
	}
	s.patchLibrary(w, r, flash)
}

// --- reader --------------------------------------------------------------

type readerView struct {
	store.Book
	Notes []store.Note
}

func (s *Server) handleReader(w http.ResponseWriter, r *http.Request) {
	b, ok := s.book(w, r)
	if !ok {
		return
	}
	if err := s.lib.Store.MarkOpened(b.ID); err != nil {
		serverError(w, err)
		return
	}
	notes, err := s.lib.Store.Notes(b.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	s.render(w, "reader-page", readerView{Book: b, Notes: notes})
}

func (s *Server) handleFile(w http.ResponseWriter, r *http.Request) {
	b, ok := s.book(w, r)
	if !ok {
		return
	}
	p, err := s.lib.Resolve(b.Path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(p)
	if err != nil {
		http.Error(w, "file is missing from the library folder", http.StatusNotFound)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeContent(w, r, "", info.ModTime(), f)
}

type position struct {
	Page  int `json:"page"`
	Pages int `json:"pages"`
}

// handlePosition saves the reading position. It accepts a JSON body from
// fetch() or navigator.sendBeacon (which sends text/plain).
func (s *Server) handlePosition(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var p position
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&p); err != nil {
		http.Error(w, "bad position: "+err.Error(), http.StatusBadRequest)
		return
	}
	err := s.lib.Store.SavePosition(id, p.Page, p.Pages)
	switch {
	case errors.Is(err, store.ErrNotFound):
		http.NotFound(w, r)
	case err != nil:
		http.Error(w, err.Error(), http.StatusBadRequest)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// --- notes ---------------------------------------------------------------

func (s *Server) handleAddNote(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	page, _ := strconv.Atoi(r.FormValue("page"))
	_, err := s.lib.Store.AddNote(id, page, r.FormValue("body"))
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	s.patchNotes(w, r, id, err, err == nil)
}

func (s *Server) handleDeleteNote(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	bookID, err := s.lib.Store.DeleteNote(id)
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	s.patchNotes(w, r, bookID, err, false)
}

// patchNotes re-renders the notes list over SSE. On success of an add it also
// clears the note text box.
func (s *Server) patchNotes(w http.ResponseWriter, r *http.Request, bookID int64, opErr error, clear bool) {
	notes, err := s.lib.Store.Notes(bookID)
	if err != nil {
		serverError(w, err)
		return
	}
	html, err := s.renderString("notes", readerView{Book: store.Book{ID: bookID}, Notes: notes})
	if err != nil {
		serverError(w, err)
		return
	}
	msg := ""
	if opErr != nil {
		msg = opErr.Error()
	}
	status, err := s.renderString("note-status", msg)
	if err != nil {
		serverError(w, err)
		return
	}
	sse := datastar.NewSSE(w, r)
	sse.PatchElements(html)
	sse.PatchElements(status)
	if clear {
		sse.ExecuteScript(`document.getElementById('note-body').value = ''`)
	}
}

func (s *Server) handleBookMarkdown(w http.ResponseWriter, r *http.Request) {
	b, ok := s.book(w, r)
	if !ok {
		return
	}
	notes, err := s.lib.Store.Notes(b.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	var buf bytes.Buffer
	WriteMarkdown(&buf, b.Title+" — notes", []BookNotes{{Book: b, Notes: notes}})
	sendMarkdown(w, slug(b.Title)+"-notes.md", buf.Bytes())
}

func (s *Server) handleAllMarkdown(w http.ResponseWriter, r *http.Request) {
	books, err := s.lib.Store.ListBooks("")
	if err != nil {
		serverError(w, err)
		return
	}
	var all []BookNotes
	for _, b := range books {
		notes, err := s.lib.Store.Notes(b.ID)
		if err != nil {
			serverError(w, err)
			return
		}
		if len(notes) > 0 {
			all = append(all, BookNotes{Book: b, Notes: notes})
		}
	}
	var buf bytes.Buffer
	WriteMarkdown(&buf, "Folio notes", all)
	sendMarkdown(w, "folio-notes.md", buf.Bytes())
}

func sendMarkdown(w http.ResponseWriter, name string, body []byte) {
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename=%q`, name))
	w.Write(body)
}

// --- helpers -------------------------------------------------------------

func (s *Server) book(w http.ResponseWriter, r *http.Request) (store.Book, bool) {
	id, ok := pathID(w, r)
	if !ok {
		return store.Book{}, false
	}
	b, err := s.lib.Store.GetBook(id)
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return store.Book{}, false
	}
	if err != nil {
		serverError(w, err)
		return store.Book{}, false
	}
	return b, true
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		http.NotFound(w, r)
		return 0, false
	}
	return id, true
}

func (s *Server) render(w http.ResponseWriter, name string, data any) {
	html, err := s.renderString(name, data)
	if err != nil {
		serverError(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(html))
}

func (s *Server) renderString(name string, data any) (string, error) {
	var buf bytes.Buffer
	if err := s.tmpl.ExecuteTemplate(&buf, name, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func redirectMsg(w http.ResponseWriter, r *http.Request, msg string) {
	http.Redirect(w, r, "/?msg="+url.QueryEscape(msg), http.StatusSeeOther)
}

func serverError(w http.ResponseWriter, err error) {
	log.Printf("error: %v", err)
	http.Error(w, "internal error", http.StatusInternalServerError)
}

func humanAgo(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return plural(int(d.Minutes()), "minute") + " ago"
	case d < 24*time.Hour:
		return plural(int(d.Hours()), "hour") + " ago"
	case d < 30*24*time.Hour:
		return plural(int(d.Hours()/24), "day") + " ago"
	default:
		return t.Format("Jan 2, 2006")
	}
}

func plural(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	return fmt.Sprintf("%d %ss", n, unit)
}

func humanSize(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%d KB", n>>10)
	default:
		return fmt.Sprintf("%d B", n)
	}
}

func slug(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "book"
	}
	return out
}
