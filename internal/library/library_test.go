package library

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/TylerGilman/folio/internal/store"
)

const fakePDF = "%PDF-1.4\n% test\n%%EOF\n"

func newLib(t *testing.T) *Library {
	t.Helper()
	root := t.TempDir()
	st, err := store.Open(filepath.Join(root, ".folio", "folio.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return &Library{Root: root, Store: st}
}

func writeFile(t *testing.T, root, rel string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(fakePDF), 0o644); err != nil {
		t.Fatal(err)
	}
}

func paths(t *testing.T, l *Library) []string {
	t.Helper()
	books, err := l.Store.ListBooks("")
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, b := range books {
		out = append(out, b.Path)
	}
	sort.Strings(out)
	return out
}

func TestScanFindsNestedPDFsAndSkipsOthers(t *testing.T) {
	l := newLib(t)
	writeFile(t, l.Root, "calculus_vol-1.pdf")
	writeFile(t, l.Root, "physics/Mechanics.PDF")
	writeFile(t, l.Root, "notes.txt")
	writeFile(t, l.Root, ".hidden/secret.pdf")
	writeFile(t, l.Root, ".dotfile.pdf")

	res, err := l.Scan()
	if err != nil {
		t.Fatal(err)
	}
	if res != (ScanResult{Found: 2, Added: 2}) {
		t.Fatalf("got %+v", res)
	}
	got := paths(t, l)
	if strings.Join(got, ",") != "calculus_vol-1.pdf,physics/Mechanics.PDF" {
		t.Fatalf("got %v", got)
	}

	// A second scan adds nothing; a deleted file is reported missing.
	if err := os.Remove(filepath.Join(l.Root, "calculus_vol-1.pdf")); err != nil {
		t.Fatal(err)
	}
	res, err = l.Scan()
	if err != nil {
		t.Fatal(err)
	}
	if res != (ScanResult{Found: 1, Added: 0, Missing: 1}) {
		t.Fatalf("rescan got %+v", res)
	}
}

func TestImportStoresPDFUnderUniqueSafeName(t *testing.T) {
	l := newLib(t)
	b1, err := l.Import("../../etc/Linear Algebra.pdf", strings.NewReader(fakePDF))
	if err != nil {
		t.Fatal(err)
	}
	b2, err := l.Import(`C:\Users\me\Linear Algebra.pdf`, strings.NewReader(fakePDF))
	if err != nil {
		t.Fatal(err)
	}
	if b1.Path != "Linear Algebra.pdf" || b2.Path != "Linear Algebra (2).pdf" {
		t.Fatalf("paths %q, %q", b1.Path, b2.Path)
	}
	if b1.Title != "Linear Algebra" {
		t.Fatalf("title %q", b1.Title)
	}
	for _, b := range []store.Book{b1, b2} {
		data, err := os.ReadFile(filepath.Join(l.Root, b.Path))
		if err != nil || string(data) != fakePDF {
			t.Fatalf("%s: content %q err=%v", b.Path, data, err)
		}
	}
	// Uploaded files are found by a later scan, not re-added.
	res, err := l.Scan()
	if err != nil || res.Added != 0 || res.Found != 2 {
		t.Fatalf("scan after import: %+v err=%v", res, err)
	}
}

func TestImportRejectsNonPDF(t *testing.T) {
	l := newLib(t)
	_, err := l.Import("fake.pdf", strings.NewReader("<html>not a pdf</html>"))
	if !errors.Is(err, ErrNotPDF) {
		t.Fatalf("got %v", err)
	}
	if got := paths(t, l); len(got) != 0 {
		t.Fatalf("store has %v", got)
	}
	entries, _ := os.ReadDir(l.Root)
	for _, e := range entries {
		if !e.IsDir() {
			t.Fatalf("left file %s in library", e.Name())
		}
	}
}

func TestResolveStaysInsideRoot(t *testing.T) {
	l := newLib(t)
	p, err := l.Resolve("physics/a.pdf")
	if err != nil || p != filepath.Join(l.Root, "physics", "a.pdf") {
		t.Fatalf("got %q err=%v", p, err)
	}
	for _, bad := range []string{"../a.pdf", "/etc/passwd", "a/../../b.pdf", ""} {
		if _, err := l.Resolve(bad); err == nil {
			t.Errorf("Resolve(%q) accepted", bad)
		}
	}
}

func TestTitleFromPath(t *testing.T) {
	for in, want := range map[string]string{
		"math/linear_algebra-3rd.pdf": "linear algebra 3rd",
		"Organic Chemistry.PDF":       "Organic Chemistry",
		"___.pdf":                     "___.pdf",
	} {
		if got := TitleFromPath(in); got != want {
			t.Errorf("TitleFromPath(%q) = %q, want %q", in, got, want)
		}
	}
}
