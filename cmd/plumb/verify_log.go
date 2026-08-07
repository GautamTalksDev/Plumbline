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
		bundle   string
		pubPath  string
		pubHex   string
		maxEnt   int
	)
	cmd := &cobra.Command{
		Use:   "verify-log",
		Short: "Verify a remote transparency log or static signed bundle",
		Long: `Fetch checkpoints and Merkle proofs from a log HTTP API or a static
publish-bundle directory/URL, verify Ed25519 signatures and consistency
proofs, and report whether the log has been tampered with.

  plumb verify-log --url https://log.example.com
  plumb verify-log --bundle ./site/log
  plumb verify-log --bundle https://example.com/log

Works against ANY endpoint that implements the Plumbline log HTTP API
(see docs/VERIFY.md) — not only the official Plumbline deployment.
The --bundle path reuses the same verification functions; it is a fetch
path only.

Exit codes:
  0  verification succeeded
  1  tampering or verification failure detected
  2  operational error (network, bad flags)`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if endpoint == "" && bundle == "" {
				return fmt.Errorf("verify-log: --url or --bundle is required")
			}
			if endpoint != "" && bundle != "" {
				return fmt.Errorf("verify-log: use only one of --url or --bundle")
			}
			pub, err := resolveVerifyPubkey(pubPath, pubHex)
			if err != nil {
				return err
			}

			var res verify.Result
			if bundle != "" {
				res, err = verify.VerifyBundle(context.Background(), verify.BundleOptions{
					Bundle:     bundle,
					PublicKey:  pub,
					MaxEntries: maxEnt,
				})
			} else {
				res, err = verify.Verify(context.Background(), verify.Options{
					URL:        endpoint,
					PublicKey:  pub,
					MaxEntries: maxEnt,
				})
			}
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
	cmd.Flags().StringVar(&endpoint, "url", "", "base URL of the log HTTP API")
	cmd.Flags().StringVar(&bundle, "bundle", "", "path or URL of a static publish-bundle (directory containing checkpoints.json)")
	cmd.Flags().StringVar(&pubPath, "pubkey", "", "path to Ed25519 public key (raw or hex)")
	cmd.Flags().StringVar(&pubHex, "pubkey-hex", "", "Ed25519 public key as hex")
	cmd.Flags().IntVar(&maxEnt, "max-entries", 500, "max inclusion proofs / leaves to check")
	return cmd
}

func resolveVerifyPubkey(pubPath, pubHex string) (ed25519.PublicKey, error) {
	if pubHex != "" {
		b, err := hex.DecodeString(strings.TrimSpace(pubHex))
		if err != nil || len(b) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("verify-log: --pubkey-hex invalid")
		}
		return b, nil
	}
	if pubPath != "" {
		b, err := os.ReadFile(pubPath) //nolint:gosec // G304 -- operator-supplied Ed25519 public key path
		if err != nil {
			return nil, err
		}
		b = []byte(strings.TrimSpace(string(b)))
		if len(b) == ed25519.PublicKeySize {
			return b, nil
		}
		if len(b) == hex.EncodedLen(ed25519.PublicKeySize) {
			out := make([]byte, ed25519.PublicKeySize)
			if _, err := hex.Decode(out, b); err != nil {
				return nil, err
			}
			return out, nil
		}
		return nil, fmt.Errorf("verify-log: bad pubkey file")
	}
	home, _ := os.UserHomeDir()
	p := filepath.Join(home, ".plumbline", "signing.ed25519.pub")
	if b, err := os.ReadFile(p); err == nil { //nolint:gosec // G304 -- default pubkey under ~/.plumbline
		b = []byte(strings.TrimSpace(string(b)))
		if len(b) == ed25519.PublicKeySize {
			return b, nil
		}
	}
	return nil, nil
}

func writeVerifyHuman(w interface{ Write([]byte) (int, error) }, res verify.Result) {
	fmt.Fprintln(w, "plumb verify-log")
	fmt.Fprintf(w, "endpoint     %s\n", safetext.String(res.Endpoint))
	fmt.Fprintf(w, "pubkey       %s\n", safetext.String(res.PubkeyHex))
	fmt.Fprintf(w, "tree_size    %d\n", res.TreeSize)
	fmt.Fprintf(w, "root_hash    %s\n", safetext.String(res.RootHash))
	fmt.Fprintf(w, "checkpoint   %v\n", res.CheckpointOK)
	fmt.Fprintf(w, "consistency  %v\n", res.ConsistencyOK)
	fmt.Fprintf(w, "inclusion    %s\n", formatInclusionLine(res))
	fmt.Fprintf(w, "leaf_hashes  %s\n", formatLeafHashesLine(res))
	if res.OK {
		fmt.Fprintln(w, "result       OK — no tampering detected")
	} else {
		fmt.Fprintln(w, "result       FAIL — tampering or inconsistency detected")
		for _, e := range res.Errors {
			fmt.Fprintf(w, "  • %s\n", safetext.String(e))
		}
	}
}

func formatInclusionLine(res verify.Result) string {
	if res.InclusionSkipped {
		reason := res.InclusionSkipReason
		if reason == "" {
			reason = "0 proofs found"
		}
		return fmt.Sprintf("skipped (%s)", reason)
	}
	if res.InclusionProofs > 0 {
		return fmt.Sprintf("%v (%d of %d proofs checked)", res.InclusionOK, res.CheckedEntries, res.InclusionProofs)
	}
	return fmt.Sprintf("%v (%d entries checked)", res.InclusionOK, res.CheckedEntries)
}

func formatLeafHashesLine(res verify.Result) string {
	if res.EntriesHashedSkipped {
		return "skipped (not evaluated)"
	}
	return fmt.Sprintf("%v", res.EntriesHashedOK)
}
