package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/GautamTalksDev/Plumbline/internal/check"
	"github.com/GautamTalksDev/Plumbline/internal/safetext"
)

const privacyGuarantee = `Privacy: plumb check makes ZERO network requests other than to the MCP
servers you have already configured. No telemetry, no version check,
no analytics, no phone-home.`

func main() {
	var jsonOut bool

	root := &cobra.Command{
		Use:   "plumb",
		Short: "Plumbline — MCP tool-metadata integrity observer",
		Long: strings.TrimSpace(`
Plumbline observes post-publication changes in MCP server tool metadata.

` + privacyGuarantee + `

Exit codes for plumb check:
  0  clean (no drift)
  1  drift detected
  2  operational error
`),
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}
	root.PersistentFlags().BoolVar(&jsonOut, "json", false, "emit machine-readable JSON")

	root.AddCommand(newCheckCmd(&jsonOut))
	root.AddCommand(newAcceptCmd(&jsonOut))
	root.AddCommand(newCrawlCmd(&jsonOut))
	root.AddCommand(newVerifyLogCmd(&jsonOut))

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(check.ExitError)
	}
}

func newCheckCmd(jsonOut *bool) *cobra.Command {
	return &cobra.Command{
		Use:   "check",
		Short: "Compare local MCP tool metadata against the baseline",
		Long: strings.TrimSpace(`
Discover MCP servers from Claude Desktop / Cursor configs, list their tools,
canonicalize and hash each tool, then compare to the local baseline.

On first run, writes a baseline and reports "baseline created".

` + privacyGuarantee + `

Exit codes:
  0  clean
  1  drift
  2  error
`),
		RunE: func(cmd *cobra.Command, args []string) error {
			res, err := check.Run(context.Background(), check.Options{})
			if err != nil && res.ExitCode == 0 {
				res.ExitCode = check.ExitError
			}
			writeCheckOutput(cmd.OutOrStdout(), cmd.ErrOrStderr(), res, *jsonOut, err)
			os.Exit(res.ExitCode)
			return nil
		},
	}
}

func newAcceptCmd(jsonOut *bool) *cobra.Command {
	return &cobra.Command{
		Use:   "accept <server>/<tool>",
		Short: "Accept the current tool metadata into the baseline",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			server, tool, ok := splitRef(args[0])
			if !ok {
				return fmt.Errorf("want <server>/<tool>, got %q", args[0])
			}
			if err := check.Accept(context.Background(), server, tool, check.Options{}); err != nil {
				fmt.Fprintln(cmd.ErrOrStderr(), err)
				os.Exit(check.ExitError)
			}
			if *jsonOut {
				_ = json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]string{
					"accepted": server + "/" + tool,
					"status":   "ok",
				})
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "accepted %s/%s into baseline\n",
					safetext.String(server), safetext.String(tool))
			}
			return nil
		},
	}
}

func splitRef(ref string) (server, tool string, ok bool) {
	i := strings.IndexByte(ref, '/')
	if i <= 0 || i == len(ref)-1 {
		return "", "", false
	}
	return ref[:i], ref[i+1:], true
}

func writeCheckOutput(stdout, stderr io.Writer, res check.Result, asJSON bool, runErr error) {
	if asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(res)
		if runErr != nil {
			fmt.Fprintln(stderr, runErr)
		}
		return
	}

	color := useColor(stdout)
	out := stdout

	fmt.Fprintln(out, "plumb check")
	fmt.Fprintln(out, safetext.String(privacyGuarantee))
	fmt.Fprintln(out)

	if res.BaselineCreated {
		printLine(out, color, colorGreen, "baseline created")
		fmt.Fprintf(out, "  path  %s\n", safetext.String(res.BaselinePath))
	} else {
		fmt.Fprintf(out, "baseline  %s\n", safetext.String(res.BaselinePath))
	}
	fmt.Fprintf(out, "servers   %d found\n", res.ServersFound)
	if res.ServersFound == 0 {
		fmt.Fprintln(out)
		fmt.Fprintln(out, "No MCP servers found in local configs.")
		fmt.Fprintln(out, "Looked in:")
		for _, p := range res.ScannedConfigs {
			fmt.Fprintf(out, "  • %s\n", safetext.String(p))
		}
		fmt.Fprintln(out, "Add an MCP server in Claude Desktop or Cursor, then re-run.")
	}
	fmt.Fprintln(out)

	for _, o := range res.Observations {
		switch o.Status {
		case "clean":
			printLine(out, color, colorGreen, fmt.Sprintf("ok    %s/%s", safetext.String(o.Server), safetext.String(o.Tool)))
		case "new":
			printLine(out, color, colorCyan, fmt.Sprintf("new   %s/%s", safetext.String(o.Server), safetext.String(o.Tool)))
			fmt.Fprintf(out, "        hash %s\n", o.Hash)
		case "drift":
			printLine(out, color, colorRed, fmt.Sprintf("DRIFT %s/%s", safetext.String(o.Server), safetext.String(o.Tool)))
			fmt.Fprintf(out, "        old  %s\n", o.OldHash)
			fmt.Fprintf(out, "        new  %s\n", o.Hash)
			if o.Severity != "" {
				fmt.Fprintf(out, "        severity %s  rules %v\n", o.Severity, o.Rules)
			}
			if o.Diff != "" {
				fmt.Fprintln(out, "        diff:")
				for _, line := range strings.Split(o.Diff, "\n") {
					fmt.Fprintf(out, "          %s\n", safetext.String(line))
				}
			}
			if desc := toolDescription(o.Raw); desc != "" {
				fmt.Fprintf(out, "        description: %s\n", safetext.String(desc))
			}
		case "error":
			label := o.Server
			if o.Tool != "" {
				label += "/" + o.Tool
			}
			printLine(out, color, colorYellow, fmt.Sprintf("error %s", safetext.String(label)))
			fmt.Fprintf(out, "        %s\n", safetext.String(o.Error))
		}
	}

	fmt.Fprintln(out)
	fmt.Fprintf(out, "summary  clean=%d  drift=%d  new=%d  error=%d  (%dms)\n",
		res.CleanCount, res.DriftCount, res.NewCount, res.ErrorCount, res.DurationMS)
	switch res.ExitCode {
	case check.ExitClean:
		printLine(out, color, colorGreen, "result  clean")
	case check.ExitDrift:
		printLine(out, color, colorRed, "result  drift — run: plumb accept <server>/<tool>")
	default:
		printLine(out, color, colorYellow, "result  error")
	}
	if runErr != nil {
		fmt.Fprintln(stderr, safetext.String(runErr.Error()))
	}
}

func toolDescription(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var v struct {
		Description string `json:"description"`
	}
	_ = json.Unmarshal(raw, &v)
	return v.Description
}

func useColor(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}

const (
	colorReset  = "\033[0m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorCyan   = "\033[36m"
)

func printLine(w io.Writer, color bool, code, msg string) {
	if color {
		fmt.Fprintf(w, "%s%s%s\n", code, msg, colorReset)
		return
	}
	fmt.Fprintln(w, msg)
}
