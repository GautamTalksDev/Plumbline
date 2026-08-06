// Command plumbd serves the read-only Plumbline transparency log HTTP API
// and optional static site. No writes. No auth. No user accounts.
package main

import (
	"crypto/ed25519"
	"encoding/hex"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/GautamTalksDev/Plumbline/internal/httpserver"
	plog "github.com/GautamTalksDev/Plumbline/internal/log"
)

func main() {
	var (
		addr     = flag.String("addr", ":8080", "listen address")
		logPath  = flag.String("log", "", "path to Merkle log sqlite (required)")
		pubPath  = flag.String("pubkey", "", "Ed25519 public key path (32 raw bytes or hex); default ~/.plumbline/signing.ed25519.pub")
		siteDir  = flag.String("site", "", "optional static site directory to serve")
		ratePerM = flag.Int("rate", 120, "max requests per minute per client IP")
	)
	flag.Parse()

	if *logPath == "" {
		fmt.Fprintln(os.Stderr, "plumbd: -log is required")
		os.Exit(2)
	}
	if *pubPath == "" {
		home, _ := os.UserHomeDir()
		*pubPath = filepath.Join(home, ".plumbline", "signing.ed25519.pub")
	}

	pub, err := loadPub(*pubPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "plumbd: pubkey: %v\n", err)
		os.Exit(2)
	}

	lg, err := plog.Open(*logPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "plumbd: open log: %v\n", err)
		os.Exit(2)
	}
	defer lg.Close()

	cfg := httpserver.Config{
		Log:      lg,
		PubKey:   pub,
		RatePerM: *ratePerM,
	}
	if *siteDir != "" {
		cfg.Site = os.DirFS(*siteDir)
	} else if st, err := os.Stat("site"); err == nil && st.IsDir() {
		cfg.Site = os.DirFS("site")
	}

	srv := httpserver.New(cfg)
	log.Printf("plumbd listening on %s (read-only)", *addr)
	if cfg.Site != nil {
		log.Printf("serving static site")
	}
	httpSrv := &http.Server{
		Addr:              *addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 16,
	}
	if err := httpSrv.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}

func loadPub(path string) (ed25519.PublicKey, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	b = []byte(strings.TrimSpace(string(b)))
	if len(b) == ed25519.PublicKeySize {
		return ed25519.PublicKey(b), nil
	}
	// Hex encoding (64 chars).
	if len(b) == hex.EncodedLen(ed25519.PublicKeySize) {
		out := make([]byte, ed25519.PublicKeySize)
		if _, err := hex.Decode(out, b); err != nil {
			return nil, err
		}
		return ed25519.PublicKey(out), nil
	}
	return nil, fmt.Errorf("%s: want %d raw bytes or %d hex chars, got %d", path, ed25519.PublicKeySize, hex.EncodedLen(ed25519.PublicKeySize), len(b))
}

// Silence unused import if Site embedding is unused in some builds.
var _ fs.FS
