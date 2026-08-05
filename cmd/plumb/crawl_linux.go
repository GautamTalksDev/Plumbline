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
		statePath      string
		logPath        string
		optOutPath     string
		registryURL    string
		keyPath        string
		summaryPath    string
		cohortPath     string
		concurrency    int
		tier2Conc      int
		requestDelay   time.Duration
		includeRemotes bool
	)

	cmd := &cobra.Command{
		Use:   "crawl",
		Short: "Operator: two-tier crawl of the public MCP registry (Linux only)",
		Long: `Two-tier weekly crawl (packages population):

  Tier 1 — version poll of npm/PyPI metadata (concurrency 8, no sandbox)
  Tier 2 — sandboxed spawn for version-changed servers ∪ control cohort

Remotes-only servers are excluded unless --include-remotes is set.
Read docs/CRAWLING.md and docs/COHORT.md before running.
Requires PLUMBLINE_CRAWL_I_UNDERSTAND=1.`,
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
			if cohortPath == "" {
				cohortPath = defaultCohortPath()
			}

			priv, err := loadSigningKey(keyPath)
			if err != nil {
				return err
			}

			sum, err := crawler.Run(context.Background(), crawler.Config{
				RegistryURL:      registryURL,
				StatePath:        statePath,
				LogPath:          logPath,
				OptOutPath:       optOutPath,
				SummaryPath:      summaryPath,
				CohortPath:       cohortPath,
				SigningKey:       priv,
				Concurrency:      concurrency,
				Tier2Concurrency: tier2Conc,
				RequestDelay:     requestDelay,
				IncludeRemotes:   includeRemotes,
			})
			writeCrawlOutput(cmd.OutOrStdout(), sum, *jsonOut, err)
			if err != nil {
				os.Exit(2)
			}
			if !sum.Complete {
				os.Exit(3)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&statePath, "state", "", "crawl resume state path (default ~/.plumbline/crawl-state.json)")
	cmd.Flags().StringVar(&logPath, "log", "", "Merkle log sqlite path (default ~/.plumbline/crawl-log.sqlite)")
	cmd.Flags().StringVar(&optOutPath, "opt-out", "", "opt-out list path (default docs/opt-out.txt or ~/.plumbline/opt-out.txt)")
	cmd.Flags().StringVar(&registryURL, "registry", crawler.DefaultRegistryURL, "MCP registry base URL")
	cmd.Flags().StringVar(&keyPath, "signing-key", "", "Ed25519 private key path (default ~/.plumbline/signing.ed25519)")
	cmd.Flags().StringVar(&summaryPath, "summary", "", "atomic summary.json output path")
	cmd.Flags().StringVar(&cohortPath, "cohort", "", "frozen cohort member list (default docs/cohort-v1-members.txt)")
	cmd.Flags().IntVar(&concurrency, "concurrency", 8, "max concurrent tier-1 version polls")
	cmd.Flags().IntVar(&tier2Conc, "tier2-concurrency", 4, "max concurrent tier-2 sandboxed spawns")
	cmd.Flags().DurationVar(&requestDelay, "delay", 250*time.Millisecond, "polite delay before each registry page fetch")
	cmd.Flags().BoolVar(&includeRemotes, "include-remotes", false, "also crawl remotes-only servers (off by default)")
	return cmd
}

func defaultCohortPath() string {
	candidates := []string{
		"docs/cohort-v1-members.txt",
		filepath.Join("docs", "cohort-v1-members.txt"),
	}
	for _, p := range candidates {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return "docs/cohort-v1-members.txt"
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
	fmt.Fprintf(w, "run_id      %s\n", safetext.String(sum.RunID))
	fmt.Fprintf(w, "complete    %v (counts_toward_six=%v)\n", sum.Complete, sum.CountsTowardSix)
	fmt.Fprintf(w, "resumed     %v\n", sum.Resumed)
	fmt.Fprintf(w, "servers     seen=%d done=%d tools=%d remotes_excluded=%d tier2=%d\n",
		sum.ServersSeen, sum.ServersDone, sum.ToolsLogged, sum.RemotesExcluded, sum.Tier2Spawned)
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
