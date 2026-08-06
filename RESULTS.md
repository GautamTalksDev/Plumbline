# Plumbline results

Kill tests KT-1 and KT-2 are pre-registered in the README. Evidence collection
runs over six weekly crawls. Status: **not yet evaluable**. This file will
state PASS or FAIL once six signed checkpoints and a hand-labelled MEDIUM+
set exist.

Do not adjust the thresholds. Do not refit taxonomy-v1 to interim data.

## Gate 5 carry-forward limitation (Round 7, 2026-08-06)

Week-1 evidence starts with Gate 5 **not fully cleared**. Pre-registration
[`docs/ROUND7-PREREG.md`](docs/ROUND7-PREREG.md); baseline dryrun6; residual
composition in [`docs/COHORT.md`](docs/COHORT.md).

| Gate | Result | Value |
|------|--------|------:|
| G5a unexplained `exec_failed` ≤5% | FAIL | 37/479 = 7.7% |
| G5b no blind process rows | PASS | 0 |
| G5c observable ≥200 | PASS | 307 |
| G5d ok ≥90% of observable | FAIL | 234/307 = 76.2% |

Round 7 bound: no residual-tuned spawn matchers; conditional
`protocol_broken` exclusion **not activated** after hand-probes found
sandbox-side failures inside the `protocol_error` sample. The 37
`exec_failed` are characterized in aggregate in `docs/COHORT.md` and remain
`exec_failed` in the log.

This limitation is stated here so later PASS/FAIL on KT-1/KT-2 is read
against an honest denominator, not a classifier tuned until the gate
number looked right.
