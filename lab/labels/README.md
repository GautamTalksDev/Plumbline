# Hand labels (KT-2 ground truth)

Every change the classifier rates **MEDIUM or above** must be reviewed by a
human during Phase E. This directory is that evidence.

## Weekly procedure

1. After the crawl run finishes:
   ```bash
   go run ./scripts/export-medium-plus.go ~/.plumbline/crawl-log.sqlite lab/labels/queue
   ```
2. Open the new `lab/labels/queue/YYYY-MM-DD-medium-plus.jsonl`.
3. For **each** line, set:
   - `label`: one of `correct` | `false_positive` | `missed`
   - `notes`: short plain-text reason (required if not `correct`)
4. Move the finished file to `lab/labels/done/YYYY-MM-DD-medium-plus.jsonl`.

## Rubric

| Label | Meaning |
|-------|---------|
| `correct` | Severity and fired rules match your reading of the before/after tool metadata. |
| `false_positive` | Rated MEDIUM+ but is cosmetic, benign, or otherwise over-severe. |
| `missed` | Rated MEDIUM+ for the wrong reason, under-states a worse issue, or omits a rule that should have fired. Use notes. |

Do not delete queue rows. Empty `label` means unfinished — KT-2 is not ready.

## What this is for

Kill test KT-2 needs a hand-labelled set, not vibes. After six weeks, score
precision on cosmetic-vs-capability using these labels plus the full change
list (see `lab/kt-eval.txt`).
