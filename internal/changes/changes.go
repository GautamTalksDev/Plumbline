// Package changes detects post-publication tool-metadata diffs in a Merkle log.
package changes

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/GautamTalksDev/Plumbline/internal/canonical"
	"github.com/GautamTalksDev/Plumbline/internal/classify"
	"github.com/GautamTalksDev/Plumbline/internal/log"
)

// Change is one detected hash change for a server/tool pair.
type Change struct {
	ServerID   string                `json:"server_id"`
	ToolName   string                `json:"tool_name"`
	OldIdx     int64                 `json:"old_idx"`
	NewIdx     int64                 `json:"new_idx"`
	OldHash    string                `json:"old_hash"`
	NewHash    string                `json:"new_hash"`
	ObservedAt int64                 `json:"observed_at"`
	Severity   classify.Severity     `json:"severity"`
	Rules      []string              `json:"rules"`
	Diff       []classify.Change     `json:"diff,omitempty"`
}

// Options filters Detect results.
type Options struct {
	Since     int64             // only changes where new_idx >= Since
	Severity  classify.Severity // minimum severity (empty = all including NONE)
	Limit     int
	SkipCrawl bool // skip _crawl_status tool rows
}

// Detect walks the log and finds consecutive hash changes per server/tool.
func Detect(entries []log.StoredEntry, opt Options) ([]Change, error) {
	if opt.Limit <= 0 {
		opt.Limit = 200
	}
	if opt.SkipCrawl {
		// default true for public site
	}
	type key struct{ s, t string }
	last := map[key]log.StoredEntry{}
	var out []Change

	for _, e := range entries {
		if e.ToolName == "_crawl_status" {
			continue
		}
		k := key{e.ServerID, e.ToolName}
		prev, ok := last[k]
		last[k] = e
		if !ok || prev.Hash == e.Hash {
			continue
		}
		if e.Idx < opt.Since {
			continue
		}

		oldTool, err := canonical.ToolJSON(prev.Canonical)
		if err != nil {
			continue
		}
		newTool, err := canonical.ToolJSON(e.Canonical)
		if err != nil {
			continue
		}
		cl, err := classify.Assess(json.RawMessage(oldTool), json.RawMessage(newTool))
		if err != nil {
			return nil, fmt.Errorf("changes: assess %s/%s: %w", e.ServerID, e.ToolName, err)
		}
		if opt.Severity != "" && classify.SeverityRank(cl.Severity) < classify.SeverityRank(opt.Severity) {
			continue
		}
		diff, _ := classify.Diff(json.RawMessage(oldTool), json.RawMessage(newTool))
		out = append(out, Change{
			ServerID:   e.ServerID,
			ToolName:   e.ToolName,
			OldIdx:     prev.Idx,
			NewIdx:     e.Idx,
			OldHash:    prev.Hash,
			NewHash:    e.Hash,
			ObservedAt: e.ObservedAt,
			Severity:   cl.Severity,
			Rules:      cl.Rules,
			Diff:       diff,
		})
	}

	sort.SliceStable(out, func(i, j int) bool {
		return out[i].NewIdx > out[j].NewIdx
	})
	if len(out) > opt.Limit {
		out = out[:opt.Limit]
	}
	return out, nil
}

// ParseSeverity accepts NONE|LOW|MEDIUM|HIGH|CRITICAL (case-insensitive).
func ParseSeverity(s string) (classify.Severity, error) {
	s = strings.ToUpper(strings.TrimSpace(s))
	if s == "" {
		return "", nil
	}
	switch classify.Severity(s) {
	case classify.SevNone, classify.SevLow, classify.SevMedium, classify.SevHigh, classify.SevCritical:
		return classify.Severity(s), nil
	default:
		return "", fmt.Errorf("unknown severity %q", s)
	}
}
