# Plumbline Severity Taxonomy

**Version:** `taxonomy-v1`  
**Status:** Normative for `internal/classify`  
**Date:** 2026-08-05

This document is the source of truth for classifying MCP tool-metadata
changes by severity. An engineer who has never read the Go implementation
MUST be able to reimplement the classifier from this specification alone
and obtain identical results on the fixtures under `testdata/taxonomy/`.

No rule may use a language model, an embedding, a classifier trained on
weights, or a probability threshold. All matching is deterministic string
and structure comparison.

---

## 1. Inputs and outputs

### 1.1 Inputs

Two JSON values representing the **same tool** before and after an
observation (typically the raw tool objects retained by Plumbline, or
their canonical forms). Key order MUST NOT affect results: implementations
MUST compare objects as unordered maps after JSON parse.

### 1.2 Output

```text
Classification {
  Severity   : NONE | LOW | MEDIUM | HIGH | CRITICAL
  Rules      : ordered list of rule IDs that fired (e.g. ["R03","R06"])
  Rationale  : optional human notes; not required for fixture equality
}
```

- `Rules` MUST be sorted ascending by rule ID (`R01` < `R02` < …).
- Duplicate IDs MUST NOT appear.
- If no rule fires, `Severity` is `NONE` and `Rules` is empty.
  (R09 is the explicit cosmetic rule; see §4.9. When R09 fires,
  `Rules` contains `["R09"]` and severity is `NONE`.)

### 1.3 Aggregation

```
Severity(change set) = max({ Severity(r) | r ∈ Rules that fired })
```

Numeric order for `max`:

| Severity | Rank |
|----------|------|
| NONE     | 0    |
| LOW      | 1    |
| MEDIUM   | 2    |
| HIGH     | 3    |
| CRITICAL | 4    |

---

## 2. Shared definitions

### 2.1 Human-readable fields

A string value is **human-readable** if its JSON Pointer path matches any of:

- `/title`
- `/description`
- `/inputSchema/description`
- `/outputSchema/description`
- any path ending in `/description` (property descriptions, etc.)
- `/annotations` string values (rare; included if present)

Non-string leaves (numbers, booleans, null) are never human-readable.
Object/array nodes are walked; only string leaves are considered text.

### 2.2 Structural diff

Implementations MUST first compute a structural change list equivalent to
`classify.Diff` (JSON Pointer paths, kinds Added/Removed/Modified). Rules
may consult either the raw before/after values or that change list.

### 2.3 Word tokenization

Words are maximal substrings of non-whitespace Unicode code points.
Whitespace is any code point with Unicode property White_Space.
Empty strings tokenize to an empty word list.

### 2.4 Word-level distance

Let `A` and `B` be word lists for old and new text.

```
denom = max(len(A), len(B))
if denom == 0: distance = 0
else: distance = edit_distance_words(A, B) / denom
```

`edit_distance_words` is the classical Levenshtein distance on the word
tokens (unit cost insert/delete/substitute). Distance is a real in
`[0, 1]`. Comparisons against `0.20` use exact rational comparison
after computing with integer arithmetic:

```
distance > 0.20  ⇔  edit_distance_words * 5 > denom
distance ≥ 0.20  ⇔  edit_distance_words * 5 ≥ denom
```

R08 uses **≥ 0.20**. R09’s word-distance cosmetic branch uses **< 0.20**.
Equality at exactly 0.20 is therefore R08, not R09.

### 2.5 Newly present

A substring, pattern match, or character class is **newly present** in a
field if it occurs in the new field value and does **not** occur in the
old field value. If the path is Added (no old value), any match in the
new value counts as newly present.

For whole-tool scans: collect the set of matches in all human-readable
fields of old vs new; a match is newly present if it appears in the new
set and not in the old set.

---

## 3. Data files (versioned, reviewable)

### 3.1 Imperative phrase list — `taxonomy/imperatives-v1.txt`

- One phrase per line, UTF-8.
- Lines that are empty or whose first non-whitespace character is `#`
  are ignored.
- Matching is **case-insensitive** substring search on human-readable
  fields (after NFC is NOT required; compare on the field string as
  stored in the tool JSON).
- Phrases may contain spaces (`do not tell`).

This file is data, not code. Changes to it MUST be reviewable in a
ordinary text diff.

### 3.2 Capability-increasing annotation directions — §4.7 table

Fixed in this document (not a separate file).

---

## 4. Rules

Each rule lists: ID, severity, rationale, precise firing condition,
and non-firing (near-miss) notes.

### R01 — CRITICAL — Invisible payload introduced

**Rationale.** Zero-width and bidi overrides hide instructions from human
reviewers while remaining visible to models. HTML/XML comments similarly
smuggle content past casual reading.

**Invisible code points** (any of these in a human-readable string):

| Code point | Name |
|------------|------|
| U+200B | ZERO WIDTH SPACE |
| U+200C | ZERO WIDTH NON-JOINER |
| U+200D | ZERO WIDTH JOINER |
| U+200E | LEFT-TO-RIGHT MARK |
| U+200F | RIGHT-TO-LEFT MARK |
| U+202A | LEFT-TO-RIGHT EMBEDDING |
| U+202B | RIGHT-TO-LEFT EMBEDDING |
| U+202C | POP DIRECTIONAL FORMATTING |
| U+202D | LEFT-TO-RIGHT OVERRIDE |
| U+202E | RIGHT-TO-LEFT OVERRIDE |
| U+2066 | LEFT-TO-RIGHT ISOLATE |
| U+2067 | RIGHT-TO-LEFT ISOLATE |
| U+2068 | FIRST STRONG ISOLATE |
| U+2069 | POP DIRECTIONAL ISOLATE |
| U+FEFF | ZERO WIDTH NO-BREAK SPACE / BOM |
| U+00AD | SOFT HYPHEN |

**HTML/XML comment marker:** the substring `<!--` (case-sensitive ASCII).

**Fires when:** for any human-readable field path, the new value contains
an invisible code point or `<!--` that is not present in the old value
at that path (or the path is Added).

**Near-miss:** old and new both contain the same U+200B → does not fire.

---

### R02 — CRITICAL — New imperative instruction to the model

**Rationale.** Tool descriptions are attacker-controlled context. New
second-person imperatives are a structural signal of prompt injection,
not a semantic judgment.

**Fires when:** any phrase from `taxonomy/imperatives-v1.txt` is newly
present (substring, case-insensitive) in any human-readable field.

**Near-miss:** phrase already present in the old description → does not fire.

---

### R03 — HIGH — New URL, domain, IP, or absolute path

**Rationale.** Exfiltrating endpoints and local paths expand the tool’s
effective trust boundary.

**Patterns** (search human-readable fields and also any string leaf in
the tool object, including schema defaults/examples):

1. **URL:** `(?i)\bhttps?://[^\s\"'<>]+`  
2. **Absolute Unix path:** `(^|[\s\"'=(])(/(?:[^\s\"']+))` — capture the
   path token starting with `/` followed by at least one non-space
   character; exclude lone `/`.  
3. **Absolute Windows path:** `(?i)\b[a-z]:\\[^\s\"']+`  
4. **IPv4 literal:** `\b((?:25[0-5]|2[0-4]\d|[01]?\d?\d)(?:\.(?:25[0-5]|2[0-4]\d|[01]?\d?\d)){3})\b`  
5. **Domain-like (no scheme):** `(?i)\b(?:[a-z0-9-]+\.)+(?:com|net|org|io|dev|ai|example|localhost)\b`

A match is the exact matched substring. R03 fires if any match is newly
present in the set of all matches over the scanned strings of the new
tool versus the old tool.

**Near-miss:** `https://api.example.com` present in both old and new →
does not fire even if surrounding text changes.

---

### R04 — HIGH — New required inputSchema property

**Rationale.** New required inputs change the call contract and can force
clients to supply attacker-chosen fields.

**Fires when:** the structural diff contains an `Added` change whose path
matches `/inputSchema/required/<n>` for integer `n`, OR a value appears
in the new `/inputSchema/required` array that is absent from the old
array (order-insensitive set comparison).

**Near-miss:** reordering `required` without membership change → no fire.
Adding an optional property only → R06, not R04.

---

### R05 — HIGH — Cross-server / cross-tool reference

**Rationale.** Mentions of other tools or servers are a shadowing and
confused-deputy indicator.

**Fires when:** any of the following is newly present in a human-readable
field:

1. Substring `mcp://` (case-insensitive).
2. Regex `(?i)\b([a-z][\w-]{2,})/([a-z][\w-]{2,})\b` — slash-separated
   `server/tool` token. The left segment MUST NOT be one of
   `com|net|org|io|dev|ai|example|localhost|www`. Matches that occur as a
   substring of an R03 URL match or absolute filesystem path match are ignored.
3. Case-insensitive phrases (fixed):
   - `another tool`
   - `other server`
   - `instead use`
   - `delegate to`
   - `call the`

**Near-miss:** `server/tool` token already in old text → does not fire.

---

### R06 — MEDIUM — New optional inputSchema property

**Rationale.** Optional fields expand the attack surface more weakly than
required fields but still change capability.

**Fires when:** structural diff contains an `Added` change under
`/inputSchema/properties/<name>` (the property object itself added), AND
that `<name>` is not a member of the new `required` set (if it is
required, R04 covers it; R06 still MAY also fire — implementations MUST
fire R04; firing both R04 and R06 for the same property is allowed but
fixtures for pure optional adds expect only R06).

**Normative clarification for fixtures:** if a newly added property name
is in `required`, emit **R04 only** (not R06). If it is not in `required`,
emit **R06**.

---

### R07 — MEDIUM — Annotation change toward greater capability

**Rationale.** MCP annotations advertise safety posture; relaxing them is
capability-relevant.

**Watched keys** under `/annotations/`:

| Key | Old → New that fires |
|-----|----------------------|
| `readOnlyHint` | `true` → `false` |
| `destructiveHint` | `false` → `true` |
| `idempotentHint` | `true` → `false` |
| `openWorldHint` | `false` → `true` |

Also fires if a watched key is **Added** with the “greater capability”
boolean value (`readOnlyHint=false`, `destructiveHint=true`,
`idempotentHint=false`, `openWorldHint=true`).

**Near-miss:** `readOnlyHint` `false` → `true` (more restrictive) → no R07.

---

### R08 — LOW — Large description rewrite without higher signals

**Rationale.** Large prose changes deserve review even when no CRITICAL/HIGH
pattern matched; they are still weaker than schema/capability changes.

**Fires when:**

1. None of R01–R07 fired, AND
2. For the `/description` field (if both sides are strings), word-level
   distance `≥ 0.20` per §2.4.

If `/description` is missing on either side, R08 does not fire from
description distance (other human-readable fields do not trigger R08 in
taxonomy-v1).

---

### R09 — NONE — Cosmetic-only change

**Rationale.** Explicitly label harmless drift so dashboards are not silent.

**Fires when:**

1. None of R01–R08 fired, AND
2. The structural diff is non-empty, AND
3. Every change is cosmetic as defined below.

**Cosmetic changes** (all must hold for every change in the set):

- Paths are only among human-readable string fields (or `/title`), AND
- For each such field, either:
  - word-level distance `< 0.20`, OR
  - the only differences are in whitespace, punctuation, or letter case
    after lowercasing and removing all characters except Unicode letters
    and digits (alnum-fold equality).

If the structural diff is empty, nothing fires (Severity NONE, Rules `[]`).

If non-cosmetic structural changes exist (e.g. schema) but somehow no
R01–R08 fired, taxonomy-v1 still emits R09 only when condition 3 holds;
otherwise Severity NONE with empty Rules (should not occur if R04–R07
are implemented correctly).

---

## 5. Evaluation algorithm

```
changes ← Diff(old, new)
fired ← empty set

if R01_matches(old, new): fired.add(R01)
if R02_matches(old, new): fired.add(R02)
if R03_matches(old, new): fired.add(R03)
if R04_matches(old, new, changes): fired.add(R04)
if R05_matches(old, new): fired.add(R05)
if R06_matches(old, new, changes): fired.add(R06)
if R07_matches(old, new, changes): fired.add(R07)

if fired ∩ {R01…R07} = ∅ and R08_matches(old, new): fired.add(R08)
if fired ∩ {R01…R08} = ∅ and R09_matches(old, new, changes): fired.add(R09)

Severity ← max severity of fired (or NONE if empty)
Rules ← sort(fired)
```

Rules R01–R07 are evaluated independently (a change set may cite several).
R08 and R09 are gated on the absence of higher rules as specified.

---

## 6. Fixtures

Directory: `testdata/taxonomy/`

Each fixture directory contains:

| File | Content |
|------|---------|
| `old.json` | before tool object |
| `new.json` | after tool object |
| `want.json` | `{"severity":"…","rules":["Rxx",…]}` |

Naming: `R0N_NN_slug` (e.g. `R03_05_near_miss_existing_url`).

taxonomy-v1 requires **at least five fixtures per rule R01–R09** (≥45 total),
including near-misses that must **not** false-positive the named rule.

---

## 7. Versioning

This document is `taxonomy-v1`. Breaking changes to rule semantics require
`taxonomy-v2` and a new imperatives file if phrases change meaning.
Additive phrase-list edits within v1 are allowed and MUST be called out in
the commit message; fixture updates MUST accompany phrase additions that
change classification results.
