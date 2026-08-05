// Package classify diffs two tool JSON objects into a deterministic,
// ordered list of structural changes.
package classify

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Kind classifies a single change.
type Kind string

const (
	Added    Kind = "Added"
	Removed  Kind = "Removed"
	Modified Kind = "Modified"
)

// Change is one structural difference at a JSON pointer path.
type Change struct {
	Path string // JSON pointer, e.g. /inputSchema/properties/path
	Kind Kind
	Old  string
	New  string
}

// Diff compares two tool JSON objects and returns changes sorted by Path
// (stable for equal paths, preserving generation order).
//
// Inputs should be canonical tool objects; key order in the raw JSON does
// not affect the result because objects are compared as maps.
func Diff(oldTool, newTool json.RawMessage) ([]Change, error) {
	var oldV, newV any
	if err := json.Unmarshal(oldTool, &oldV); err != nil {
		return nil, fmt.Errorf("classify: old tool: %w", err)
	}
	if err := json.Unmarshal(newTool, &newV); err != nil {
		return nil, fmt.Errorf("classify: new tool: %w", err)
	}

	var out []Change
	diffValue("", oldV, newV, &out)

	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Path < out[j].Path
	})
	return out, nil
}

func diffValue(ptr string, oldV, newV any, out *[]Change) {
	if oldV == nil && newV == nil {
		return
	}
	if oldV == nil {
		*out = append(*out, Change{Path: ptrOrRoot(ptr), Kind: Added, New: render(newV)})
		return
	}
	if newV == nil {
		*out = append(*out, Change{Path: ptrOrRoot(ptr), Kind: Removed, Old: render(oldV)})
		return
	}

	switch o := oldV.(type) {
	case map[string]any:
		n, ok := newV.(map[string]any)
		if !ok {
			*out = append(*out, Change{Path: ptrOrRoot(ptr), Kind: Modified, Old: render(oldV), New: render(newV)})
			return
		}
		diffObject(ptr, o, n, out)
	case []any:
		n, ok := newV.([]any)
		if !ok {
			*out = append(*out, Change{Path: ptrOrRoot(ptr), Kind: Modified, Old: render(oldV), New: render(newV)})
			return
		}
		diffArray(ptr, o, n, out)
	case string:
		ns, ok := newV.(string)
		if !ok {
			*out = append(*out, Change{Path: ptrOrRoot(ptr), Kind: Modified, Old: render(oldV), New: render(newV)})
			return
		}
		if isDescriptionPath(ptr) {
			diffDescription(ptr, o, ns, out)
			return
		}
		if o != ns {
			*out = append(*out, Change{Path: ptrOrRoot(ptr), Kind: Modified, Old: o, New: ns})
		}
	default:
		if !jsonEqual(oldV, newV) {
			*out = append(*out, Change{Path: ptrOrRoot(ptr), Kind: Modified, Old: render(oldV), New: render(newV)})
		}
	}
}

func diffObject(ptr string, oldObj, newObj map[string]any, out *[]Change) {
	keys := make(map[string]struct{}, len(oldObj)+len(newObj))
	for k := range oldObj {
		keys[k] = struct{}{}
	}
	for k := range newObj {
		keys[k] = struct{}{}
	}
	sorted := make([]string, 0, len(keys))
	for k := range keys {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)

	for _, k := range sorted {
		child := joinPointer(ptr, k)
		ov, oOK := oldObj[k]
		nv, nOK := newObj[k]
		switch {
		case oOK && !nOK:
			*out = append(*out, Change{Path: child, Kind: Removed, Old: render(ov)})
		case !oOK && nOK:
			*out = append(*out, Change{Path: child, Kind: Added, New: render(nv)})
		default:
			diffValue(child, ov, nv, out)
		}
	}
}

func diffArray(ptr string, oldArr, newArr []any, out *[]Change) {
	max := len(oldArr)
	if len(newArr) > max {
		max = len(newArr)
	}
	for i := 0; i < max; i++ {
		child := ptr + "/" + strconv.Itoa(i)
		switch {
		case i >= len(oldArr):
			*out = append(*out, Change{Path: child, Kind: Added, New: render(newArr[i])})
		case i >= len(newArr):
			*out = append(*out, Change{Path: child, Kind: Removed, Old: render(oldArr[i])})
		default:
			diffValue(child, oldArr[i], newArr[i], out)
		}
	}
}

func isDescriptionPath(ptr string) bool {
	return ptr == "/description" || strings.HasSuffix(ptr, "/description")
}

// diffDescription emits word-level changes so a typo fix is one small Modified.
func diffDescription(ptr string, oldS, newS string, out *[]Change) {
	if oldS == newS {
		return
	}
	oldW := splitWords(oldS)
	newW := splitWords(newS)
	ops := myersWordDiff(oldW, newW)

	// Collapse into hunks of contiguous equal-kind ops for stable, small output.
	type hunk struct {
		kind     Kind
		old, neu []string
	}
	var hunks []hunk
	var cur *hunk
	finish := func() {
		if cur == nil {
			return
		}
		hunks = append(hunks, *cur)
		cur = nil
	}
	for _, op := range ops {
		switch op.tag {
		case opKeep:
			finish()
		case opReplace:
			if cur != nil && cur.kind == Modified {
				cur.old = append(cur.old, op.old)
				cur.neu = append(cur.neu, op.neu)
			} else {
				finish()
				cur = &hunk{kind: Modified, old: []string{op.old}, neu: []string{op.neu}}
			}
		case opInsert:
			if cur != nil && cur.kind == Added {
				cur.neu = append(cur.neu, op.neu)
			} else {
				finish()
				cur = &hunk{kind: Added, neu: []string{op.neu}}
			}
		case opDelete:
			if cur != nil && cur.kind == Removed {
				cur.old = append(cur.old, op.old)
			} else {
				finish()
				cur = &hunk{kind: Removed, old: []string{op.old}}
			}
		}
	}
	finish()

	for _, h := range hunks {
		*out = append(*out, Change{
			Path: ptr,
			Kind: h.kind,
			Old:  strings.Join(h.old, " "),
			New:  strings.Join(h.neu, " "),
		})
	}
}

type opTag int

const (
	opKeep opTag = iota
	opInsert
	opDelete
	opReplace
)

type wordOp struct {
	tag      opTag
	old, neu string
}

// myersWordDiff returns a simple edit script using LCS length DP.
func myersWordDiff(a, b []string) []wordOp {
	n, m := len(a), len(b)
	// dp[i][j] = LCS length of a[i:] and b[j:]
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else {
				dp[i][j] = max(dp[i+1][j], dp[i][j+1])
			}
		}
	}

	var ops []wordOp
	i, j := 0, 0
	for i < n && j < m {
		if a[i] == b[j] {
			ops = append(ops, wordOp{tag: opKeep, old: a[i], neu: b[j]})
			i++
			j++
			continue
		}
		if dp[i+1][j] >= dp[i][j+1] {
			// Prefer delete when equal — deterministic.
			// Lookahead: if next of a matches b, delete; if next of b matches a, insert;
			// if both differ, emit replace when both sides advance is better.
			if dp[i+1][j] > dp[i][j+1] {
				ops = append(ops, wordOp{tag: opDelete, old: a[i]})
				i++
			} else if dp[i][j+1] > dp[i+1][j] {
				ops = append(ops, wordOp{tag: opInsert, neu: b[j]})
				j++
			} else {
				// Equal LCS: try replace (delete+insert collapsed).
				ops = append(ops, wordOp{tag: opReplace, old: a[i], neu: b[j]})
				i++
				j++
			}
		} else {
			ops = append(ops, wordOp{tag: opInsert, neu: b[j]})
			j++
		}
	}
	for ; i < n; i++ {
		ops = append(ops, wordOp{tag: opDelete, old: a[i]})
	}
	for ; j < m; j++ {
		ops = append(ops, wordOp{tag: opInsert, neu: b[j]})
	}
	return ops
}

func splitWords(s string) []string {
	var words []string
	start := -1
	for i, r := range s {
		if unicode.IsSpace(r) {
			if start >= 0 {
				words = append(words, s[start:i])
				start = -1
			}
			continue
		}
		if start < 0 {
			start = i
		}
		_ = utf8.RuneLen(r)
	}
	if start >= 0 {
		words = append(words, s[start:])
	}
	return words
}

func joinPointer(parent, key string) string {
	return parent + "/" + escapePointerToken(key)
}

func escapePointerToken(s string) string {
	s = strings.ReplaceAll(s, "~", "~0")
	s = strings.ReplaceAll(s, "/", "~1")
	return s
}

func ptrOrRoot(ptr string) string {
	if ptr == "" {
		return ""
	}
	return ptr
}

func render(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case nil:
		return "null"
	default:
		b, err := json.Marshal(x)
		if err != nil {
			return fmt.Sprintf("%v", v)
		}
		return string(b)
	}
}

func jsonEqual(a, b any) bool {
	ab, err1 := json.Marshal(a)
	bb, err2 := json.Marshal(b)
	if err1 != nil || err2 != nil {
		return false
	}
	return string(ab) == string(bb)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
