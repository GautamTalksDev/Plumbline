package main

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/GautamTalksDev/Plumbline/internal/safetext"
	"github.com/GautamTalksDev/Plumbline/internal/verify"
)

func newVerifyLogCmd(jsonOut *bool) *cobra.Command {
	var (
		endpoint string
		pubPath  string
		pubHex   string
		maxEnt   int
	)
	cmd := &cobra.Command{
		Use:   "verify-log",
		Short: "Verify a remote transparency log (any conforming endpoint)",
		Long: `Fetch checkpoints and Merkle proofs from a log HTTP API, verify Ed25519
signatures and inclusion/consistency proofs, and report whether the log has
been tampered with.

Works against ANY endpoint that implements the Plumbline log HTTP API
(see docs/VERIFY.md) — not only the official Plumbline deployment.

Exit codes:
  0  verification succeeded
  1  tampering or verification failure detected
  2  operational error (network, bad flags)`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if endpoint == "" {
				return fmt.Errorf("verify-log: --url is required")
			}
			var pub ed25519.PublicKey
			if pubHex != "" {
				b, err := hex.DecodeString(strings.TrimSpace(pubHex))
				if err != nil || len(b) != ed25519.PublicKeySize {
					return fmt.Errorf("verify-log: --pubkey-hex invalid")
				}
				pub = b
			} else if pubPath != "" {
				b, err := os.ReadFile(pubPath)
				if err != nil {
					return err
				}
				b = []byte(strings.TrimSpace(string(b)))
				if len(b) == ed25519.PublicKeySize {
					pub = b
				} else if len(b) == hex.EncodedLen(ed25519.PublicKeySize) {
					out := make([]byte, ed25519.PublicKeySize)
					if _, err := hex.Decode(out, b); err != nil {
						return err
					}
					pub = out
				} else {
					return fmt.Errorf("verify-log: bad pubkey file")
				}
			} else {
				// Try local default; else verify will fetch /pubkey from endpoint.
				home, _ := os.UserHomeDir()
				p := filepath.Join(home, ".plumbline", "signing.ed25519.pub")
				if b, err := os.ReadFile(p); err == nil {
					b = []byte(strings.TrimSpace(string(b)))
					if len(b) == ed25519.PublicKeySize {
						pub = b
					}
				}
			}

			res, err := verify.Verify(context.Background(), verify.Options{
				URL:       endpoint,
				PublicKey: pub,
				MaxEntries: maxEnt,
			})
			if err != nil {
				fmt.Fprintln(cmd.ErrOrStderr(), err)
				os.Exit(2)
			}
			if *jsonOut {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				_ = enc.Encode(res)
			} else {
				writeVerifyHuman(cmd.OutOrStdout(), res)
			}
			if !res.OK {
				os.Exit(1)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&endpoint, "url", "", "base URL of the log HTTP API (required)")
	cmd.Flags().StringVar(&pubPath, "pubkey", "", "path to Ed25519 public key (raw or hex)")
	cmd.Flags().StringVar(&pubHex, "pubkey-hex", "", "Ed25519 public key as hex")
	cmd.Flags().IntVar(&maxEnt, "max-entries", 500, "max leaves to download and re-hash")
	return cmd
}

func writeVerifyHuman(w interface{ Write([]byte) (int, error) }, res verify.Result) {
	fmt.Fprintln(w, "plumb verify-log")
	fmt.Fprintf(w, "endpoint     %s\n", safetext.String(res.Endpoint))
	fmt.Fprintf(w, "pubkey       %s\n", safetext.String(res.PubkeyHex))
	fmt.Fprintf(w, "tree_size    %d\n", res.TreeSize)
	fmt.Fprintf(w, "root_hash    %s\n", safetext.String(res.RootHash))
	fmt.Fprintf(w, "checkpoint   %v\n", res.CheckpointOK)
	fmt.Fprintf(w, "consistency  %v\n", res.ConsistencyOK)
	fmt.Fprintf(w, "inclusion    %v (%d entries checked)\n", res.InclusionOK, res.CheckedEntries)
	fmt.Fprintf(w, "leaf_hashes  %v\n", res.EntriesHashedOK)
	if res.OK {
		fmt.Fprintln(w, "result       OK — no tampering detected")
	} else {
		fmt.Fprintln(w, "result       FAIL — tampering or inconsistency detected")
		for _, e := range res.Errors {
			fmt.Fprintf(w, "  • %s\n", safetext.String(e))
		}
	}
}
