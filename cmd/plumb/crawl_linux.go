//go:build linux

package main

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/GautamTalksDev/Plumbline/internal/crawler"
	"github.com/GautamTalksDev/Plumbline/internal/safetext"
)

func newCrawlCmd(jsonOut *bool) *cobra.Command {
	var (
		statePath    string
		logPath      string
		optOutPath   string
		registryURL  string
		keyPath      string
		concurrency  int
		requestDelay time.Duration
	)

	cmd := &cobra.Command{
		Use:   "crawl",
		Short: "Operator: sandboxed crawl of the public MCP registry (Linux only)",
		Long: `Enumerate servers from the official MCP registry, observe each inside an
ephemeral sandbox, append every outcome to the Merkle log, and sign a checkpoint.

Read docs/CRAWLING.md before running. Requires PLUMBLINE_CRAWL_I_UNDERSTAND=1.
Never executes server code outside the sandbox.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			home, _ := os.UserHomeDir()
			plumDir := filepath.Join(home, ".plumbline")
			if statePath == "" {
				statePath = filepath.Join(plumDir, "crawl-state.json")
			}
			if logPath == "" {
				logPath = filepath.Join(plumDir, "crawl-log.sqlite")
			}
			if optOutPath == "" {
				optOutPath = defaultOptOutPath()
			}
			if keyPath == "" {
				keyPath = filepath.Join(plumDir, "signing.ed25519")
			}

			priv, err := loadSigningKey(keyPath)
			if err != nil {
				return err
			}

			sum, err := crawler.Run(context.Background(), crawler.Config{
				RegistryURL:  registryURL,
				StatePath:    statePath,
				LogPath:      logPath,
				OptOutPath:   optOutPath,
				SigningKey:   priv,
				Concurrency:  concurrency,
				RequestDelay: requestDelay,
			})
			writeCrawlOutput(cmd.OutOrStdout(), sum, *jsonOut, err)
			if err != nil {
				os.Exit(2)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&statePath, "state", "", "crawl resume state path (default ~/.plumbline/crawl-state.json)")
	cmd.Flags().StringVar(&logPath, "log", "", "Merkle log sqlite path (default ~/.plumbline/crawl-log.sqlite)")
	cmd.Flags().StringVar(&optOutPath, "opt-out", "", "opt-out list path (default docs/opt-out.txt or ~/.plumbline/opt-out.txt)")
	cmd.Flags().StringVar(&registryURL, "registry", crawler.DefaultRegistryURL, "MCP registry base URL")
	cmd.Flags().StringVar(&keyPath, "signing-key", "", "Ed25519 private key path (default ~/.plumbline/signing.ed25519)")
	cmd.Flags().IntVar(&concurrency, "concurrency", 4, "max concurrent sandboxed probes")
	cmd.Flags().DurationVar(&requestDelay, "delay", 250*time.Millisecond, "polite delay before each registry page fetch")
	return cmd
}

func defaultOptOutPath() string {
	candidates := []string{
		"docs/opt-out.txt",
		filepath.Join("docs", "opt-out.txt"),
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, ".plumbline", "opt-out.txt"))
	}
	for _, p := range candidates {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".plumbline", "opt-out.txt")
	}
	return "opt-out.txt"
}

func loadSigningKey(path string) (ed25519.PrivateKey, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("crawl: signing key %s: %w (run scripts/gen-signing-key.sh)", path, err)
	}
	if len(b) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("crawl: signing key %s: want %d bytes, got %d", path, ed25519.PrivateKeySize, len(b))
	}
	return ed25519.PrivateKey(b), nil
}

func writeCrawlOutput(w io.Writer, sum crawler.Summary, asJSON bool, runErr error) {
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		_ = enc.Encode(sum)
		return
	}
	fmt.Fprintln(w, "plumb crawl")
	fmt.Fprintf(w, "resumed     %v\n", sum.Resumed)
	fmt.Fprintf(w, "servers     seen=%d done=%d tools=%d\n", sum.ServersSeen, sum.ServersDone, sum.ToolsLogged)
	fmt.Fprintf(w, "duration    %s\n", sum.Duration)
	fmt.Fprintln(w, "events:")
	for ev, n := range sum.EventCounts {
		fmt.Fprintf(w, "  %-16s %d\n", safetext.String(ev), n)
	}
	if sum.Checkpoint != nil {
		fmt.Fprintf(w, "checkpoint  tree_size=%d root=%s\n", sum.Checkpoint.TreeSize, sum.Checkpoint.RootHash)
	}
	if runErr != nil {
		fmt.Fprintf(w, "error       %s\n", safetext.String(runErr.Error()))
	}
}
