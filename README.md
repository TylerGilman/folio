# Folio

Personal, **local-first** PDF study library. Open source (MIT).

Organize textbook PDFs, resume where you left off, and take page-linked notes. Built for Tyler; others can self-host or run locally. Not a cloud textbook farm — files stay on your machine (or storage you control).

Nereus / personal tool. Working title; rename anytime.

> **Only add files you have the right to use.** Folio never ships, uploads or syncs PDFs. Your library folder is yours, and it is gitignored in this repo.

## Run

Requires Go 1.25+. No Node, no build step: PDF.js and Datastar are vendored in `web/static/vendor`.

```sh
go run ./cmd/folio
```

Open <http://127.0.0.1:8080>. On first start Folio creates `./library` (gitignored). Copy PDFs into it and press **Rescan folder**, or use **Upload PDFs**.

### Point Folio at an existing PDF folder

```sh
FOLIO_LIBRARY=~/Documents/Textbooks go run ./cmd/folio
# or
go run ./cmd/folio -library ~/Documents/Textbooks
```

Folio scans the folder and its subfolders for `*.pdf` on start and whenever you press **Rescan folder**. It never moves, renames or deletes your files. Uploads are written into the library folder; an existing file is never overwritten.

Metadata (reading position, tags, notes) lives in a SQLite file at `<library>/.folio/folio.db`, so it travels with the folder. Back up that file to keep your notes.

### Settings

| Env var         | Flag       | Default                     | Meaning                                  |
| --------------- | ---------- | --------------------------- | ---------------------------------------- |
| `FOLIO_LIBRARY` | `-library` | `./library`                 | Folder of PDFs                           |
| `FOLIO_DB`      | `-db`      | `<library>/.folio/folio.db` | Metadata database                        |
| `FOLIO_ADDR`    | `-addr`    | `127.0.0.1:8080`            | Listen address (`0.0.0.0:8080` for LAN)  |

Folio has no accounts or passwords. If you listen on your LAN, anyone on that network can read and upload to your library.

### Build a binary

```sh
go build -o bin/folio ./cmd/folio
./bin/folio -library ~/Documents/Textbooks
```

## Features (v1)

- **Library**: grid of books with first-page covers, progress bar, page `x of y`, last opened. Filter by title or tag (`/` focuses the filter).
- **Shelves**: tag any book (comma-separated for several); each tag becomes a shelf in the sidebar.
- **Reader**: PDF.js, one page at a time, next/prev, jump to page, zoom. Position saves after you stop paging and again when you leave the page.
- **Notes**: note text tied to the current page; click a note's page to jump there. Export one book's notes or all notes as Markdown.
- **Missing files**: books whose file left the folder stay listed (with notes) and are marked missing; they come back when the file returns.

### Reader keys

| Key                          | Action                   |
| ---------------------------- | ------------------------ |
| `Right` `j` `PageDown` `Space` | Next page              |
| `Left` `k` `PageUp` `Shift+Space` | Previous page       |
| `Home` / `End`               | First / last page        |
| `g`                          | Jump to page             |
| `+` `-` `0`                  | Zoom in / out / fit page |
| `n`                          | Write a note             |
| `Ctrl+Enter`                 | Save the note            |
| `Shift+N`                    | Show / hide notes        |
| `Esc`                        | Back to the library      |

## Layout

```
cmd/folio/          server entry point (flags, env, startup scan)
internal/store/     SQLite metadata: books, positions, tags, notes
internal/library/   folder scan, safe upload import, path resolution
internal/server/    HTTP handlers, Datastar SSE patches, Markdown export
web/templates/      html/template pages
web/static/         CSS, reader/library JS, vendored PDF.js + Datastar
library/            your PDFs (gitignored)
```

## Develop

```sh
go test ./...
```

Vendored front-end libraries: PDF.js 6.4.299 (`pdfjs-dist`, Apache-2.0) and Datastar 1.0.4 (MIT). Their licenses sit next to the files.

## Goals

- Import PDFs into a library (folders / tags / shelves)
- Remember reading position + % progress per book
- Notes and highlights tied to page (export Markdown)
- Fast keyboard-friendly UI
- Datastar server for the first version; PDFs served from local disk

## Non-goals (v1)

- Hosting other people’s copyrighted textbooks in the cloud
- Multi-tenant SaaS
- AI chat sidebar
- Mobile apps

## Docs

- `docs/mvp-brief.md` — one-shot build brief

## License

MIT
