// Command folio runs the local-first Folio PDF study library.
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/TylerGilman/folio/internal/library"
	"github.com/TylerGilman/folio/internal/server"
	"github.com/TylerGilman/folio/internal/store"
)

func main() {
	root := flag.String("library", envOr("FOLIO_LIBRARY", "./library"), "folder of PDFs (env FOLIO_LIBRARY)")
	dbPath := flag.String("db", os.Getenv("FOLIO_DB"), "metadata database (env FOLIO_DB; default <library>/.folio/folio.db)")
	addr := flag.String("addr", envOr("FOLIO_ADDR", "127.0.0.1:8080"), "listen address (env FOLIO_ADDR)")
	flag.Parse()

	absRoot, err := filepath.Abs(*root)
	if err != nil {
		log.Fatal(err)
	}
	if err := os.MkdirAll(absRoot, 0o755); err != nil {
		log.Fatalf("library folder: %v", err)
	}
	if *dbPath == "" {
		*dbPath = filepath.Join(absRoot, ".folio", "folio.db")
	}

	st, err := store.Open(*dbPath)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer st.Close()

	lib := &library.Library{Root: absRoot, Store: st}
	res, err := lib.Scan()
	if err != nil {
		log.Printf("initial scan: %v", err)
	}
	log.Printf("library %s: %d PDFs (%d new, %d missing)", absRoot, res.Found, res.Added, res.Missing)

	srv, err := server.New(lib)
	if err != nil {
		log.Fatal(err)
	}
	httpSrv := &http.Server{Addr: *addr, Handler: srv, ReadHeaderTimeout: 10 * time.Second}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		httpSrv.Shutdown(shutdown)
	}()

	log.Printf("Folio on http://%s", *addr)
	if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
