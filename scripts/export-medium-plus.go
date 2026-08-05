//go:build ignore

// Export MEDIUM+ changes from a crawl log for hand labelling (KT-2).
// Usage: go run ./scripts/export-medium-plus.go <log.sqlite> <out-dir>
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/GautamTalksDev/Plumbline/internal/changes"
	"github.com/GautamTalksDev/Plumbline/internal/classify"
	"github.com/GautamTalksDev/Plumbline/internal/log"
)

type labelRow struct {
	ExportedAt string            `json:"exported_at"`
	ServerID   string            `json:"server_id"`
	ToolName   string            `json:"tool_name"`
	OldIdx     int64             `json:"old_idx"`
	NewIdx     int64             `json:"new_idx"`
	OldHash    string            `json:"old_hash"`
	NewHash    string            `json:"new_hash"`
	ObservedAt int64             `json:"observed_at"`
	Severity   classify.Severity `json:"severity"`
	Rules      []string          `json:"rules"`
	// Hand fields — fill weekly:
	Label string `json:"label"` // correct | false_positive | missed
	Notes string `json:"notes"`
}

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintf(os.Stderr, "usage: export-medium-plus log.sqlite out-dir\n")
		os.Exit(2)
	}
	logPath, outDir := os.Args[1], os.Args[2]
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		fatal(err)
	}
	lg, err := log.Open(logPath)
	if err != nil {
		fatal(err)
	}
	defer lg.Close()
	size, err := lg.Size()
	if err != nil {
		fatal(err)
	}
	ents, err := lg.Entries(0, size)
	if err != nil {
		fatal(err)
	}
	ch, err := changes.Detect(ents, changes.Options{
		Severity: classify.SevMedium,
		Limit:    10000,
	})
	if err != nil {
		fatal(err)
	}

	day := time.Now().UTC().Format("2006-01-02")
	outPath := filepath.Join(outDir, day+"-medium-plus.jsonl")
	f, err := os.Create(outPath)
	if err != nil {
		fatal(err)
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	now := time.Now().UTC().Format(time.RFC3339)
	for _, c := range ch {
		row := labelRow{
			ExportedAt: now,
			ServerID:   c.ServerID,
			ToolName:   c.ToolName,
			OldIdx:     c.OldIdx,
			NewIdx:     c.NewIdx,
			OldHash:    c.OldHash,
			NewHash:    c.NewHash,
			ObservedAt: c.ObservedAt,
			Severity:   c.Severity,
			Rules:      c.Rules,
			Label:      "",
			Notes:      "",
		}
		if err := enc.Encode(row); err != nil {
			fatal(err)
		}
	}
	fmt.Printf("wrote %d MEDIUM+ rows to %s\n", len(ch), outPath)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
