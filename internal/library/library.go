// Package library maps the PDF files under the library root to store records.
package library

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/TylerGilman/folio/internal/store"
)

// ErrNotPDF is returned when uploaded content does not start with a PDF header.
var ErrNotPDF = errors.New("file is not a PDF")

// Library is a folder of PDFs plus the metadata store that describes them.
type Library struct {
	Root  string
	Store *store.Store
}

// ScanResult summarizes a scan.
type ScanResult struct {
	Found   int
	Added   int
	Missing int
}

// Scan walks the root, records every PDF in the store, and flags records
// whose files are gone. Hidden files and folders (dot-prefixed) are skipped.
func (l *Library) Scan() (ScanResult, error) {
	var res ScanResult
	present := map[string]bool{}
	err := filepath.WalkDir(l.Root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p != l.Root && strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() || !isPDFName(d.Name()) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(l.Root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		_, added, err := l.Store.UpsertBook(rel, TitleFromPath(rel), info.Size())
		if err != nil {
			return fmt.Errorf("record %s: %w", rel, err)
		}
		present[rel] = true
		res.Found++
		if added {
			res.Added++
		}
		return nil
	})
	if err != nil {
		return res, err
	}
	res.Missing, err = l.Store.MarkMissing(present)
	return res, err
}

// Resolve turns a stored relative path into an absolute path inside the root.
func (l *Library) Resolve(rel string) (string, error) {
	clean := path.Clean("/" + rel)[1:]
	if clean == "" || clean != rel {
		return "", fmt.Errorf("invalid library path %q", rel)
	}
	return filepath.Join(l.Root, filepath.FromSlash(clean)), nil
}

// Import writes an uploaded PDF into the root under a safe, unique file name,
// records it in the store, and returns the new book.
func (l *Library) Import(filename string, r io.Reader) (store.Book, error) {
	br := bufio.NewReader(r)
	head, _ := br.Peek(5)
	if string(head) != "%PDF-" {
		return store.Book{}, ErrNotPDF
	}
	if err := os.MkdirAll(l.Root, 0o755); err != nil {
		return store.Book{}, err
	}
	tmp, err := os.CreateTemp(l.Root, ".upload-*")
	if err != nil {
		return store.Book{}, err
	}
	defer os.Remove(tmp.Name())
	size, err := io.Copy(tmp, br)
	if err == nil {
		err = tmp.Chmod(0o644) // CreateTemp uses 0600
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return store.Book{}, err
	}
	name, err := l.claimName(SafeFileName(filename))
	if err != nil {
		return store.Book{}, err
	}
	if err := os.Rename(tmp.Name(), filepath.Join(l.Root, name)); err != nil {
		return store.Book{}, err
	}
	b, _, err := l.Store.UpsertBook(name, TitleFromPath(name), size)
	return b, err
}

// claimName picks name, or "name (2).pdf" and so on, so no file is overwritten.
func (l *Library) claimName(name string) (string, error) {
	base := strings.TrimSuffix(name, filepath.Ext(name))
	for i := 1; i < 1000; i++ {
		candidate := name
		if i > 1 {
			candidate = fmt.Sprintf("%s (%d).pdf", base, i)
		}
		if _, err := os.Lstat(filepath.Join(l.Root, candidate)); errors.Is(err, fs.ErrNotExist) {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("no free file name for %q", name)
}

func isPDFName(name string) bool {
	return strings.EqualFold(filepath.Ext(name), ".pdf")
}

// SafeFileName reduces an uploaded file name to a plain base name ending in .pdf.
func SafeFileName(name string) string {
	name = filepath.Base(filepath.ToSlash(strings.ReplaceAll(name, `\`, "/")))
	name = strings.Map(func(r rune) rune {
		if r == '/' || r == 0 || unicode.IsControl(r) {
			return -1
		}
		return r
	}, name)
	name = strings.TrimLeft(strings.TrimSpace(name), ".")
	base := strings.TrimSpace(strings.TrimSuffix(name, filepath.Ext(name)))
	if base == "" {
		base = "upload"
	}
	return base + ".pdf"
}

// TitleFromPath derives a readable title from a file path:
// "math/linear_algebra-3rd.pdf" becomes "linear algebra 3rd".
func TitleFromPath(p string) string {
	name := path.Base(p)
	name = strings.TrimSuffix(name, path.Ext(name))
	name = strings.NewReplacer("_", " ", "-", " ").Replace(name)
	name = strings.Join(strings.Fields(name), " ")
	if name == "" {
		return path.Base(p)
	}
	return name
}
