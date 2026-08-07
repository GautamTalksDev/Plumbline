package main

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/GautamTalksDev/Plumbline/internal/publish"
)

func newPublishBundleCmd(jsonOut *bool) *cobra.Command {
	var (
		logPath string
		outDir  string
		since   int64
		week    int
		pubPath string
		pubHex  string
	)
	cmd := &cobra.Command{
		Use:   "publish-bundle",
		Short: "Write a static signed-log JSON bundle for the transparency site",
		Long: `Export Merkle log checkpoints, per-server observation histories (hashes
and metadata only), and inclusion/consistency proofs as static JSON under
--out (default site/log/). No server is required to serve the bundle.

Does not change what is recorded or how it is classified.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			home, _ := os.UserHomeDir()
			if logPath == "" {
				logPath = filepath.Join(home, ".plumbline", "crawl-log.sqlite")
			}
			pub, err := loadPubkey(pubPath, pubHex)
			if err != nil {
				return fmt.Errorf("publish-bundle: %w", err)
			}
			if err := publish.Bundle(publish.Options{
				LogPath: logPath,
				OutDir:  outDir,
				Since:   since,
				Week:    week,
				PubKey:  pub,
			}); err != nil {
				return err
			}
			if *jsonOut {
				_ = json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{
					"out":  outDir,
					"week": week,
				})
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "published static bundle → %s (week %d)\n", outDir, week)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&logPath, "log", "", "Merkle log sqlite (default ~/.plumbline/crawl-log.sqlite)")
	cmd.Flags().StringVar(&outDir, "out", "site/log", "output directory")
	cmd.Flags().Int64Var(&since, "since", 0, "optional index: only rewrite server/inclusion artifacts for idx >= since")
	cmd.Flags().IntVar(&week, "week", 1, "study week N of 6 (written into meta.json)")
	cmd.Flags().StringVar(&pubPath, "pubkey", "", "path to Ed25519 public key (raw or hex)")
	cmd.Flags().StringVar(&pubHex, "pubkey-hex", "", "Ed25519 public key as hex")
	return cmd
}

func loadPubkey(path, hexStr string) (ed25519.PublicKey, error) {
	if hexStr != "" {
		b, err := hex.DecodeString(strings.TrimSpace(hexStr))
		if err != nil || len(b) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("--pubkey-hex invalid")
		}
		return b, nil
	}
	if path == "" {
		// Prefer in-repo published key, then ~/.plumbline.
		candidates := []string{
			"docs/log-public-key.ed25519",
			"site/pubkey.txt",
		}
		if home, err := os.UserHomeDir(); err == nil {
			candidates = append(candidates, filepath.Join(home, ".plumbline", "signing.ed25519.pub"))
		}
		for _, c := range candidates {
			if b, err := os.ReadFile(c); err == nil { //nolint:gosec // G304 — known pubkey locations
				if pub, err := parsePubkeyBytes(b); err == nil {
					return pub, nil
				}
			}
		}
		return nil, fmt.Errorf("--pubkey or --pubkey-hex is required")
	}
	b, err := os.ReadFile(path) //nolint:gosec // G304 — operator-supplied pubkey path
	if err != nil {
		return nil, err
	}
	return parsePubkeyBytes(b)
}

func parsePubkeyBytes(b []byte) (ed25519.PublicKey, error) {
	b = []byte(strings.TrimSpace(string(b)))
	if len(b) == ed25519.PublicKeySize {
		return ed25519.PublicKey(b), nil
	}
	if len(b) == hex.EncodedLen(ed25519.PublicKeySize) {
		out := make([]byte, ed25519.PublicKeySize)
		if _, err := hex.Decode(out, b); err != nil {
			return nil, err
		}
		return out, nil
	}
	return nil, fmt.Errorf("bad pubkey file")
}
