// Package web embeds Folio's HTML templates and static assets.
package web

import "embed"

// Templates holds the html/template files.
//
//go:embed templates/*.html
var Templates embed.FS

// Static holds CSS, JS and vendored libraries (PDF.js, Datastar).
//
//go:embed static
var Static embed.FS
