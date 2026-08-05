package classify

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

//go:embed imperatives-v1.txt
var imperativesFile string

// TaxonomyVersion is the severity taxonomy implemented by Assess.
const TaxonomyVersion = "taxonomy-v1"

// Severity is the taxonomy-v1 severity level.
type Severity string

const (
	SevNone     Severity = "NONE"
	SevLow      Severity = "LOW"
	SevMedium   Severity = "MEDIUM"
	SevHigh     Severity = "HIGH"
	SevCritical Severity = "CRITICAL"
)

var severityRank = map[Severity]int{
	SevNone:     0,
	SevLow:      1,
	SevMedium:   2,
	SevHigh:     3,
	SevCritical: 4,
}

// SeverityRank returns a comparable rank for filtering (NONE=0 … CRITICAL=4).
func SeverityRank(s Severity) int {
	return severityRank[s]
}

// Classification is the taxonomy output for a before/after tool pair.
type Classification struct {
	Severity Severity `json:"severity"`
	Rules    []string `json:"rules"`
}

// Assess applies taxonomy-v1 rules to oldTool/newTool and returns
// the max severity with the sorted list of firing rule IDs.
func Assess(oldTool, newTool json.RawMessage) (Classification, error) {
	var oldV, newV any
	if len(oldTool) > 0 {
		if err := json.Unmarshal(oldTool, &oldV); err != nil {
			return Classification{}, fmt.Errorf("classify: assess old: %w", err)
		}
	}
	if len(newTool) > 0 {
		if err := json.Unmarshal(newTool, &newV); err != nil {
			return Classification{}, fmt.Errorf("classify: assess new: %w", err)
		}
	}

	changes, err := Diff(oldTool, newTool)
	if err != nil {
		return Classification{}, err
	}

	fired := map[string]bool{}

	if ruleR01(oldV, newV) {
		fired["R01"] = true
	}
	if ruleR02(oldV, newV) {
		fired["R02"] = true
	}
	if ruleR03(oldV, newV) {
		fired["R03"] = true
	}
	if ruleR04(oldV, newV, changes) {
		fired["R04"] = true
	}
	if ruleR05(oldV, newV) {
		fired["R05"] = true
	}
	if ruleR06(oldV, newV, changes) {
		fired["R06"] = true
	}
	if ruleR07(changes) {
		fired["R07"] = true
	}

	higher := fired["R01"] || fired["R02"] || fired["R03"] || fired["R04"] || fired["R05"] || fired["R06"] || fired["R07"]
	if !higher && ruleR08(oldV, newV) {
		fired["R08"] = true
	}
	if !higher && !fired["R08"] && ruleR09(oldV, newV, changes) {
		fired["R09"] = true
	}

	rules := make([]string, 0, len(fired))
	for id := range fired {
		rules = append(rules, id)
	}
	sort.Strings(rules)

	sev := SevNone
	for _, id := range rules {
		if s := ruleSeverity(id); severityRank[s] > severityRank[sev] {
			sev = s
		}
	}
	return Classification{Severity: sev, Rules: rules}, nil
}

func ruleSeverity(id string) Severity {
	switch id {
	case "R01", "R02":
		return SevCritical
	case "R03", "R04", "R05":
		return SevHigh
	case "R06", "R07":
		return SevMedium
	case "R08":
		return SevLow
	case "R09":
		return SevNone
	default:
		return SevNone
	}
}

// --- field walking -----------------------------------------------------------

func walkStrings(v any, ptr string, fn func(ptr, s string)) {
	switch x := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			walkStrings(x[k], joinPointer(ptr, k), fn)
		}
	case []any:
		for i, el := range x {
			walkStrings(el, ptr+"/"+strconv.Itoa(i), fn)
		}
	case string:
		fn(ptr, x)
	}
}

func humanReadable(ptr string) bool {
	if ptr == "/title" || ptr == "/description" {
		return true
	}
	return strings.HasSuffix(ptr, "/description")
}

func humanReadableStrings(v any) map[string]string {
	out := map[string]string{}
	walkStrings(v, "", func(ptr, s string) {
		if humanReadable(ptr) {
			out[ptr] = s
		}
	})
	return out
}

func allStrings(v any) map[string]string {
	out := map[string]string{}
	walkStrings(v, "", func(ptr, s string) {
		out[ptr] = s
	})
	return out
}

// --- R01 ---------------------------------------------------------------------

var invisibleRunes = map[rune]bool{
	'\u200B': true, '\u200C': true, '\u200D': true,
	'\u200E': true, '\u200F': true,
	'\u202A': true, '\u202B': true, '\u202C': true, '\u202D': true, '\u202E': true,
	'\u2066': true, '\u2067': true, '\u2068': true, '\u2069': true,
	'\uFEFF': true, '\u00AD': true,
}

func invisiblePayloads(s string) map[string]bool {
	out := map[string]bool{}
	for _, r := range s {
		if invisibleRunes[r] {
			out[string(r)] = true
		}
	}
	if strings.Contains(s, "<!--") {
		out["<!--"] = true
	}
	return out
}

func ruleR01(oldV, newV any) bool {
	oldHR := humanReadableStrings(oldV)
	newHR := humanReadableStrings(newV)
	for ptr, ns := range newHR {
		np := invisiblePayloads(ns)
		op := invisiblePayloads(oldHR[ptr]) // empty if missing
		for p := range np {
			if !op[p] {
				return true
			}
		}
	}
	return false
}

// --- R02 ---------------------------------------------------------------------

var imperativePhrases = loadImperatives(imperativesFile)

func loadImperatives(data string) []string {
	var out []string
	for _, line := range strings.Split(data, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, strings.ToLower(line))
	}
	sort.Strings(out)
	return out
}

func ruleR02(oldV, newV any) bool {
	oldHR := humanReadableStrings(oldV)
	newHR := humanReadableStrings(newV)
	for ptr, ns := range newHR {
		nl := strings.ToLower(ns)
		ol := strings.ToLower(oldHR[ptr])
		for _, phrase := range imperativePhrases {
			if strings.Contains(nl, phrase) && !strings.Contains(ol, phrase) {
				return true
			}
		}
	}
	return false
}

// --- R03 ---------------------------------------------------------------------

var (
	reURL     = regexp.MustCompile(`(?i)\bhttps?://[^\s"'<>]+`)
	reWinPath = regexp.MustCompile(`(?i)\b[a-z]:\\[^\s"']+`)
	reIPv4    = regexp.MustCompile(`\b(?:(?:25[0-5]|2[0-4]\d|[01]?\d?\d)\.){3}(?:25[0-5]|2[0-4]\d|[01]?\d?\d)\b`)
	reDomain  = regexp.MustCompile(`(?i)\b(?:[a-z0-9-]+\.)+(?:com|net|org|io|dev|ai|example|localhost)\b`)
	reUnixAbs = regexp.MustCompile(`(?:^|[\s"'=(])(/(?:[^\s"']+))`)
)

func extractR03Matches(s string) map[string]bool {
	out := map[string]bool{}
	for _, m := range reURL.FindAllString(s, -1) {
		out[m] = true
	}
	for _, m := range reWinPath.FindAllString(s, -1) {
		out[m] = true
	}
	for _, m := range reIPv4.FindAllString(s, -1) {
		out[m] = true
	}
	for _, m := range reDomain.FindAllString(s, -1) {
		out[m] = true
	}
	for _, sub := range reUnixAbs.FindAllStringSubmatch(s, -1) {
		if len(sub) > 1 && sub[1] != "/" {
			out[sub[1]] = true
		}
	}
	return out
}

func collectR03(v any) map[string]bool {
	out := map[string]bool{}
	for _, s := range allStrings(v) {
		for m := range extractR03Matches(s) {
			out[m] = true
		}
	}
	return out
}

func ruleR03(oldV, newV any) bool {
	oldM := collectR03(oldV)
	newM := collectR03(newV)
	for m := range newM {
		if !oldM[m] {
			return true
		}
	}
	return false
}

// --- R04 / R06 ---------------------------------------------------------------

func requiredSet(v any) map[string]bool {
	out := map[string]bool{}
	obj, _ := v.(map[string]any)
	if obj == nil {
		return out
	}
	schema, _ := obj["inputSchema"].(map[string]any)
	if schema == nil {
		return out
	}
	req, _ := schema["required"].([]any)
	for _, x := range req {
		if s, ok := x.(string); ok {
			out[s] = true
		}
	}
	return out
}

func ruleR04(oldV, newV any, _ []Change) bool {
	oldR := requiredSet(oldV)
	newR := requiredSet(newV)
	for name := range newR {
		if !oldR[name] {
			return true
		}
	}
	return false
}

func ruleR06(oldV, newV any, changes []Change) bool {
	newReq := requiredSet(newV)
	for _, c := range changes {
		if c.Kind != Added {
			continue
		}
		// /inputSchema/properties/<name> exactly one more segment
		const prefix = "/inputSchema/properties/"
		if !strings.HasPrefix(c.Path, prefix) {
			continue
		}
		rest := c.Path[len(prefix):]
		if rest == "" || strings.Contains(rest, "/") {
			continue
		}
		name := unescapePointerToken(rest)
		if newReq[name] {
			continue // R04 only
		}
		return true
	}
	return false
}

func unescapePointerToken(s string) string {
	s = strings.ReplaceAll(s, "~1", "/")
	s = strings.ReplaceAll(s, "~0", "~")
	return s
}

// --- R05 ---------------------------------------------------------------------

var (
	// server/tool tokens: both sides ≥2 chars after first letter; exclude TLD-looking left sides.
	reServerTool = regexp.MustCompile(`(?i)\b([a-z][\w-]{2,})/([a-z][\w-]{2,})\b`)
	r05Phrases   = []string{
		"another tool",
		"other server",
		"instead use",
		"delegate to",
		"call the",
	}
	r05TLDLeft = map[string]bool{
		"com": true, "net": true, "org": true, "io": true, "dev": true,
		"ai": true, "example": true, "localhost": true, "www": true,
	}
)

func r05Matches(s string) map[string]bool {
	out := map[string]bool{}
	lower := strings.ToLower(s)
	if strings.Contains(lower, "mcp://") {
		out["mcp://"] = true
	}
	urls := map[string]bool{}
	for _, m := range reURL.FindAllString(s, -1) {
		urls[m] = true
	}
	absPaths := map[string]bool{}
	for _, sub := range reUnixAbs.FindAllStringSubmatch(s, -1) {
		if len(sub) > 1 {
			absPaths[sub[1]] = true
		}
	}
	for _, m := range reWinPath.FindAllString(s, -1) {
		absPaths[m] = true
	}
	for _, sub := range reServerTool.FindAllStringSubmatch(s, -1) {
		full := sub[0]
		left := strings.ToLower(sub[1])
		if r05TLDLeft[left] {
			continue
		}
		inside := false
		for u := range urls {
			if strings.Contains(u, full) {
				inside = true
				break
			}
		}
		if !inside {
			for p := range absPaths {
				if strings.Contains(p, full) {
					inside = true
					break
				}
			}
		}
		if inside {
			continue
		}
		out[strings.ToLower(full)] = true
	}
	for _, p := range r05Phrases {
		if strings.Contains(lower, p) {
			out[p] = true
		}
	}
	return out
}

func ruleR05(oldV, newV any) bool {
	oldHR := humanReadableStrings(oldV)
	newHR := humanReadableStrings(newV)
	for ptr, ns := range newHR {
		nm := r05Matches(ns)
		om := r05Matches(oldHR[ptr])
		for m := range nm {
			if !om[m] {
				return true
			}
		}
	}
	return false
}

// --- R07 ---------------------------------------------------------------------

func ruleR07(changes []Change) bool {
	for _, c := range changes {
		if !strings.HasPrefix(c.Path, "/annotations/") {
			continue
		}
		key := strings.TrimPrefix(c.Path, "/annotations/")
		if strings.Contains(key, "/") {
			continue
		}
		switch key {
		case "readOnlyHint":
			if (c.Kind == Modified && c.Old == "true" && c.New == "false") ||
				(c.Kind == Added && c.New == "false") {
				return true
			}
		case "destructiveHint":
			if (c.Kind == Modified && c.Old == "false" && c.New == "true") ||
				(c.Kind == Added && c.New == "true") {
				return true
			}
		case "idempotentHint":
			if (c.Kind == Modified && c.Old == "true" && c.New == "false") ||
				(c.Kind == Added && c.New == "false") {
				return true
			}
		case "openWorldHint":
			if (c.Kind == Modified && c.Old == "false" && c.New == "true") ||
				(c.Kind == Added && c.New == "true") {
				return true
			}
		}
	}
	return false
}

// --- R08 / R09 ---------------------------------------------------------------

func descriptionOf(v any) (string, bool) {
	obj, _ := v.(map[string]any)
	if obj == nil {
		return "", false
	}
	s, ok := obj["description"].(string)
	return s, ok
}

func wordEditDistance(a, b []string) int {
	n, m := len(a), len(b)
	if n == 0 {
		return m
	}
	if m == 0 {
		return n
	}
	prev := make([]int, m+1)
	cur := make([]int, m+1)
	for j := 0; j <= m; j++ {
		prev[j] = j
	}
	for i := 1; i <= n; i++ {
		cur[0] = i
		for j := 1; j <= m; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			del := prev[j] + 1
			ins := cur[j-1] + 1
			sub := prev[j-1] + cost
			cur[j] = min3(del, ins, sub)
		}
		prev, cur = cur, prev
	}
	return prev[m]
}

func wordDistanceAtLeast20(oldS, newS string) bool {
	a, b := splitWords(oldS), splitWords(newS)
	denom := len(a)
	if len(b) > denom {
		denom = len(b)
	}
	if denom == 0 {
		return false
	}
	d := wordEditDistance(a, b)
	return d*5 >= denom
}

func ruleR08(oldV, newV any) bool {
	oldS, oOK := descriptionOf(oldV)
	newS, nOK := descriptionOf(newV)
	if !oOK || !nOK {
		return false
	}
	return wordDistanceAtLeast20(oldS, newS)
}

func alnumFold(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return b.String()
}

func isCosmeticFieldChange(oldS, newS string) bool {
	if alnumFold(oldS) == alnumFold(newS) {
		return true
	}
	a, b := splitWords(oldS), splitWords(newS)
	denom := len(a)
	if len(b) > denom {
		denom = len(b)
	}
	if denom == 0 {
		return true
	}
	d := wordEditDistance(a, b)
	// distance < 0.20 ⇔ 5d < denom (strict); equal 0.20 is not < 0.20
	return d*5 < denom
}

func ruleR09(oldV, newV any, changes []Change) bool {
	if len(changes) == 0 {
		return false
	}
	oldHR := humanReadableStrings(oldV)
	newHR := humanReadableStrings(newV)
	seen := map[string]bool{}
	for _, c := range changes {
		if c.Path != "/title" && !humanReadable(c.Path) {
			return false
		}
		seen[c.Path] = true
	}
	for ptr := range seen {
		if !isCosmeticFieldChange(oldHR[ptr], newHR[ptr]) {
			return false
		}
	}
	return true
}

func min3(a, b, c int) int {
	if a <= b && a <= c {
		return a
	}
	if b <= c {
		return b
	}
	return c
}