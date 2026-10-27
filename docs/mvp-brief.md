# Folio MVP brief — local-first PDF library

## Goal

A personal Datastar web app Tyler runs on his machine (or LAN): drop textbook PDFs into a library dir, browse shelves, open a reader that **remembers page + %,** and supports **page-linked notes**. Open source; no account system required for v1.

## Stack (prefer)

- Small Go server + Datastar (same comfort zone as Govrn demo), OR Python if PDF tooling is easier — pick Go if pdfium/pdf.js client-side is enough
- Client-side PDF.js for rendering (keeps server simple; stream or static-serve files from a configured library root)
- SQLite (or JSON files) for metadata: title, path, last_page, progress, tags, notes
- Library root = env `FOLIO_LIBRARY` defaulting to `./library` (gitignored)

## Features (v1 acceptance)

1. Scan / import PDFs from `FOLIO_LIBRARY` (and optional upload into that folder)
2. Library grid/list: cover or filename, progress bar, last opened
3. Reader: PDF.js, next/prev, jump to page, persist position on leave/debounce
4. Notes panel: note text + page number; list notes for current book; export all notes as Markdown
5. Tags or simple shelves (at least one grouping mechanism)
6. README with `go run` / how to point at an existing PDF folder
7. Branch `feat/mvp` + open PR; do not merge to main until Tyler OKs (public repo — still no force-push nonsense)

## Out of scope

- Cloud sync / accounts
- OCR
- EPUB (nice later)
- Full-text search across PDFs (nice later)
- Sharing libraries publicly

## Layout

```
/docs
/cmd or / main server
/web or /static  (PDF.js, CSS)
/internal        metadata store
/library         gitignored sample path
```

## Privacy / legal

Do not ship copyrighted textbooks in the repo. `.gitignore` the library folder. README must say: only add files you have rights to use.
