// Package jsondiff produces a human-readable structural diff of two JSON values.
package jsondiff

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Diff returns indented lines describing structural differences between old
// and new JSON. Empty string means no differences (or both invalid identically).
func Diff(oldRaw, newRaw json.RawMessage) string {
	var oldV, newV any
	oldErr := json.Unmarshal(oldRaw, &oldV)
	newErr := json.Unmarshal(newRaw, &newV)
	if oldErr != nil && newErr != nil {
		return "both sides are invalid JSON"
	}
	if oldErr != nil {
		return "old value is invalid JSON"
	}
	if newErr != nil {
		return "new value is invalid JSON"
	}
	var lines []string
	walk("$", oldV, newV, &lines)
	return strings.Join(lines, "\n")
}

func walk(path string, oldV, newV any, lines *[]string) {
	switch o := oldV.(type) {
	case map[string]any:
		n, ok := newV.(map[string]any)
		if !ok {
			*lines = append(*lines, fmt.Sprintf("~ %s: object → %s", path, typeName(newV)))
			return
		}
		keys := map[string]struct{}{}
		for k := range o {
			keys[k] = struct{}{}
		}
		for k := range n {
			keys[k] = struct{}{}
		}
		sorted := make([]string, 0, len(keys))
		for k := range keys {
			sorted = append(sorted, k)
		}
		sort.Strings(sorted)
		for _, k := range sorted {
			p := path + "." + k
			ov, oOK := o[k]
			nv, nOK := n[k]
			switch {
			case oOK && !nOK:
				*lines = append(*lines, fmt.Sprintf("- %s: %s", p, preview(ov)))
			case !oOK && nOK:
				*lines = append(*lines, fmt.Sprintf("+ %s: %s", p, preview(nv)))
			default:
				walk(p, ov, nv, lines)
			}
		}
	case []any:
		n, ok := newV.([]any)
		if !ok {
			*lines = append(*lines, fmt.Sprintf("~ %s: array → %s", path, typeName(newV)))
			return
		}
		max := len(o)
		if len(n) > max {
			max = len(n)
		}
		for i := 0; i < max; i++ {
			p := fmt.Sprintf("%s[%d]", path, i)
			switch {
			case i >= len(o):
				*lines = append(*lines, fmt.Sprintf("+ %s: %s", p, preview(n[i])))
			case i >= len(n):
				*lines = append(*lines, fmt.Sprintf("- %s: %s", p, preview(o[i])))
			default:
				walk(p, o[i], n[i], lines)
			}
		}
	default:
		if !equalJSON(oldV, newV) {
			*lines = append(*lines, fmt.Sprintf("~ %s: %s → %s", path, preview(oldV), preview(newV)))
		}
	}
}

func equalJSON(a, b any) bool {
	ab, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return string(ab) == string(bb)
}

func typeName(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case bool:
		return "bool"
	case float64:
		return "number"
	case string:
		return "string"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	default:
		return fmt.Sprintf("%T", v)
	}
}

func preview(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "<unprintable>"
	}
	s := string(b)
	if len(s) > 120 {
		return s[:117] + "..."
	}
	return s
}
