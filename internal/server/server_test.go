package server

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/TylerGilman/folio/internal/library"
	"github.com/TylerGilman/folio/internal/store"
)

const fakePDF = "%PDF-1.4\n% test file body\n%%EOF\n"

type env struct {
	t   *testing.T
	lib *library.Library
	srv *httptest.Server
}

func newEnv(t *testing.T, files ...string) *env {
	t.Helper()
	root := t.TempDir()
	for _, f := range files {
		p := filepath.Join(root, f)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(fakePDF), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	st, err := store.Open(filepath.Join(root, ".folio", "folio.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	lib := &library.Library{Root: root, Store: st}
	if _, err := lib.Scan(); err != nil {
		t.Fatal(err)
	}
	s, err := New(lib)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s)
	t.Cleanup(ts.Close)
	return &env{t: t, lib: lib, srv: ts}
}

func (e *env) do(method, path, contentType string, body io.Reader) (*http.Response, string) {
	e.t.Helper()
	req, err := http.NewRequest(method, e.srv.URL+path, body)
	if err != nil {
		e.t.Fatal(err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res, string(b)
}

func (e *env) get(path string) (*http.Response, string) { return e.do("GET", path, "", nil) }

func (e *env) postForm(path string, v url.Values) (*http.Response, string) {
	return e.do("POST", path, "application/x-www-form-urlencoded", strings.NewReader(v.Encode()))
}

func TestLibraryListsScannedBooks(t *testing.T) {
	e := newEnv(t, "linear_algebra.pdf", "physics/mechanics.pdf")
	res, body := e.get("/")
	if res.StatusCode != 200 {
		t.Fatalf("status %d", res.StatusCode)
	}
	for _, want := range []string{"linear algebra", "mechanics", "Not started"} {
		if !strings.Contains(body, want) {
			t.Errorf("library page lacks %q", want)
		}
	}
}

func TestReadingPositionShowsInLibrary(t *testing.T) {
	e := newEnv(t, "book.pdf")
	res, _ := e.do("POST", "/api/books/1/position", "text/plain", strings.NewReader(`{"page":30,"pages":120}`))
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("status %d", res.StatusCode)
	}
	_, body := e.get("/")
	if !strings.Contains(body, "p. 30 of 120 · 25%") {
		t.Fatalf("library does not show saved position:\n%s", body)
	}
	// The reader resumes at the saved page.
	_, body = e.get("/read/1")
	if !strings.Contains(body, `data-page="30"`) {
		t.Fatal("reader does not start at saved page")
	}

	if res, _ := e.do("POST", "/api/books/1/position", "", strings.NewReader("nope")); res.StatusCode != 400 {
		t.Fatalf("bad body: status %d", res.StatusCode)
	}
	if res, _ := e.do("POST", "/api/books/99/position", "", strings.NewReader(`{"page":1,"pages":2}`)); res.StatusCode != 404 {
		t.Fatalf("unknown book: status %d", res.StatusCode)
	}
}

func TestFileServingSupportsRanges(t *testing.T) {
	e := newEnv(t, "book.pdf")
	req, _ := http.NewRequest("GET", e.srv.URL+"/files/1", nil)
	req.Header.Set("Range", "bytes=0-4")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusPartialContent || string(b) != "%PDF-" || res.Header.Get("Content-Type") != "application/pdf" {
		t.Fatalf("status %d type %q body %q", res.StatusCode, res.Header.Get("Content-Type"), b)
	}
	if res, _ := e.get("/files/2"); res.StatusCode != 404 {
		t.Fatalf("unknown book: status %d", res.StatusCode)
	}
	os.Remove(filepath.Join(e.lib.Root, "book.pdf"))
	if res, _ := e.get("/files/1"); res.StatusCode != 404 {
		t.Fatalf("missing file: status %d", res.StatusCode)
	}
}

func TestNotesAndMarkdownExport(t *testing.T) {
	e := newEnv(t, "Linear Algebra.pdf", "Other.pdf")
	res, body := e.postForm("/books/1/notes", url.Values{"page": {"42"}, "body": {"Eigenvalues **matter**"}})
	if res.StatusCode != 200 || !strings.Contains(body, "Eigenvalues **matter**") || !strings.Contains(body, "p. 42") {
		t.Fatalf("status %d body:\n%s", res.StatusCode, body)
	}
	e.postForm("/books/1/notes", url.Values{"page": {"3"}, "body": {"Vectors"}})
	_, body = e.postForm("/books/1/notes", url.Values{"page": {"3"}, "body": {"   "}})
	if !strings.Contains(body, "empty note") {
		t.Fatalf("empty note not reported:\n%s", body)
	}

	res, md := e.get("/books/1/notes.md")
	if !strings.HasPrefix(res.Header.Get("Content-Type"), "text/markdown") ||
		!strings.Contains(res.Header.Get("Content-Disposition"), "linear-algebra-notes.md") {
		t.Fatalf("headers %v", res.Header)
	}
	want := "# Linear Algebra — notes\n\n## Linear Algebra\n\nSource: `Linear Algebra.pdf`\n\n" +
		"### Page 3\n\nVectors\n\n### Page 42\n\nEigenvalues **matter**\n"
	if md != want {
		t.Fatalf("markdown:\n%s\nwant:\n%s", md, want)
	}

	// The all-notes export skips books without notes.
	_, all := e.get("/notes.md")
	if !strings.Contains(all, "## Linear Algebra") || strings.Contains(all, "## Other") {
		t.Fatalf("all notes:\n%s", all)
	}

	// Delete the page-3 note.
	notes, _ := e.lib.Store.Notes(1)
	res, body = e.do("DELETE", "/notes/"+itoa(notes[0].ID), "", nil)
	if res.StatusCode != 200 || strings.Contains(body, "Vectors") {
		t.Fatalf("delete: status %d body:\n%s", res.StatusCode, body)
	}
	if _, md := e.get("/books/1/notes.md"); strings.Contains(md, "Vectors") {
		t.Fatal("deleted note still exported")
	}
}

func TestTagsMakeShelves(t *testing.T) {
	e := newEnv(t, "a.pdf", "b.pdf")
	_, body := e.postForm("/books/1/tags", url.Values{"tag": {"Math, Year 2"}})
	if !strings.Contains(body, "year 2") || !strings.Contains(body, "math") {
		t.Fatalf("tag patch:\n%s", body)
	}
	_, shelf := e.get("/?tag=math")
	if !strings.Contains(shelf, `id="book-1"`) || strings.Contains(shelf, `id="book-2"`) {
		t.Fatal("shelf filter shows wrong books")
	}
	e.do("DELETE", "/books/1/tags/year%202", "", nil)
	if b, _ := e.lib.Store.GetBook(1); len(b.Tags) != 1 || b.Tags[0] != "math" {
		t.Fatalf("tags after delete: %v", b.Tags)
	}
}

func TestUploadAddsPDFsAndSkipsOthers(t *testing.T) {
	e := newEnv(t)
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("files", "My Book.pdf")
	fw.Write([]byte(fakePDF))
	fw, _ = mw.CreateFormFile("files", "evil.pdf")
	fw.Write([]byte("#!/bin/sh"))
	mw.Close()

	res, _ := e.do("POST", "/upload", mw.FormDataContentType(), &buf)
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("status %d", res.StatusCode)
	}
	msg, _ := url.QueryUnescape(strings.TrimPrefix(res.Header.Get("Location"), "/?msg="))
	if !strings.Contains(msg, "Added 1") || !strings.Contains(msg, "evil.pdf") {
		t.Fatalf("message %q", msg)
	}
	if _, err := os.Stat(filepath.Join(e.lib.Root, "My Book.pdf")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(e.lib.Root, "evil.pdf")); err == nil {
		t.Fatal("non-PDF was saved")
	}
	_, body := e.get("/")
	if !strings.Contains(body, "My Book") {
		t.Fatal("uploaded book not listed")
	}
}

func TestScanPicksUpNewFiles(t *testing.T) {
	e := newEnv(t, "a.pdf")
	os.WriteFile(filepath.Join(e.lib.Root, "new.pdf"), []byte(fakePDF), 0o644)
	_, body := e.do("POST", "/scan", "", nil)
	if !strings.Contains(body, "2 PDFs, 1 new, 0 missing") || !strings.Contains(body, `id="book-2"`) {
		t.Fatalf("scan patch:\n%s", body)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
