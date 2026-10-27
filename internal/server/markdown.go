package server

import (
	"fmt"
	"io"
	"strings"

	"github.com/TylerGilman/folio/internal/store"
)

// BookNotes groups a book with its notes for export.
type BookNotes struct {
	Book  store.Book
	Notes []store.Note
}

// WriteMarkdown writes notes as a Markdown document: one section per book,
// one sub-section per note, labelled with its page.
func WriteMarkdown(w io.Writer, title string, books []BookNotes) {
	fmt.Fprintf(w, "# %s\n", title)
	if len(books) == 0 {
		fmt.Fprint(w, "\n_No notes yet._\n")
		return
	}
	for _, bn := range books {
		fmt.Fprintf(w, "\n## %s\n\n", bn.Book.Title)
		fmt.Fprintf(w, "Source: `%s`\n", bn.Book.Path)
		if len(bn.Notes) == 0 {
			fmt.Fprint(w, "\n_No notes yet._\n")
		}
		for _, n := range bn.Notes {
			fmt.Fprintf(w, "\n### Page %d\n\n%s\n", n.Page, strings.TrimSpace(n.Body))
		}
	}
}
