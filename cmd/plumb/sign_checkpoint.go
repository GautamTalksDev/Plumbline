package main

import (
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/GautamTalksDev/Plumbline/internal/log"
)

// newSignCheckpointCmd signs the current Merkle tree head offline from the crawl box.
// The private key should live outside ~/.plumbline on the machine that executes packages.
func newSignCheckpointCmd(jsonOut *bool) *cobra.Command {
	var (
		logPath string
		keyPath string
		outPath string
	)
	cmd := &cobra.Command{
		Use:   "sign-checkpoint",
		Short: "Sign the current log tree head (run offline from the crawl key store)",
		Long: `Sign a plumbline-checkpoint-v1 over the current Merkle log head.

The crawl box should not hold the Ed25519 private key while executing stranger
packages. Prefer: run the weekly crawl with --defer-sign, then invoke this
command from a key kept offline (or briefly mounted) after the run completes.

Writes the checkpoint JSON to --out (default: stdout / lab run folder when set).`,
		RunE: func(cmd *cobra.Command, args []string) (err error) {
			home, _ := os.UserHomeDir()
			if logPath == "" {
				logPath = filepath.Join(home, ".plumbline", "crawl-log.sqlite")
			}
			if keyPath == "" {
				return fmt.Errorf("sign-checkpoint: --signing-key is required (keep it off the crawl box)")
			}
			priv, err := loadSigningKey(keyPath)
			if err != nil {
				return err
			}
			lg, err := log.Open(logPath)
			if err != nil {
				return err
			}
			defer func() {
				// Write path: a failed close means the checkpoint may not
				// have reached disk. Surface it rather than reporting success.
				if cerr := lg.Close(); cerr != nil && err == nil {
					err = cerr
				}
			}()
			cp, err := lg.SignCheckpoint(ed25519.PrivateKey(priv))
			if err != nil {
				return err
			}
			b, err := json.MarshalIndent(cp, "", "  ")
			if err != nil {
				return err
			}
			b = append(b, '\n')
			if outPath == "" {
				_, err = cmd.OutOrStdout().Write(b)
				return err
			}
			if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
				return err
			}
			return os.WriteFile(outPath, b, 0o644)
		},
	}
	cmd.Flags().StringVar(&logPath, "log", "", "Merkle log sqlite (default ~/.plumbline/crawl-log.sqlite)")
	cmd.Flags().StringVar(&keyPath, "signing-key", "", "Ed25519 private key path (offline store)")
	cmd.Flags().StringVar(&outPath, "out", "", "write checkpoint JSON here (default stdout)")
	_ = jsonOut
	return cmd
}
