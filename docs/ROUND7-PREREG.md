# Round 7 pre-registration (bound 2026-08-06, before residual inspection)

This document is the exclusion-rule lock for the final Gate-5 classifier
pass. It is committed **before** hand-probes of `malformed` /
`protocol_error` and **before** any inspection of the 37 unexplained
`exec_failed` stderr previews from dryrun6.

Stop rule (stated by the operator, not negotiable under pressure):

> One more pass. Then the clock starts regardless.
> If G5a and G5d clear — start week 1 Monday.
> If they do not clear but the residual is explained in aggregate —
> start week 1 Monday anyway, with the residual written into
> `docs/COHORT.md` and carried into `RESULTS.md` as a limitation.

## Legitimacy test (applies to every rule below)

A class is legitimate only if it names a **permanent, server-side** cause:
the server cannot be observed anonymously by anyone, ever — not just by
this crawler, this sandbox, or this configuration.

Illegitimate (do not add):

- Causes that are our sandbox, our client, or our configuration.
- Matchers kept only when they move a gate number; a rule that catches 2
  servers is kept; a rule kept only when it catches 30 was never a rule.

## R7-0 — No residual-tuned spawn matchers

**Rule:** Round 7 adds **zero** new substrings to
`internal/crawler/spawn_matchers-v1.txt` derived from reading the dryrun6
`exec_failed` residual.

**Justification:** The remaining 37 and the G5d gap (~42) overlap. Extending
matchers after inspecting that pile until the denominator clears is
goalpost-moving. Existing permanent classes
(`credentials_required`, `network_required_at_runtime`,
`runtime_incompatible`, `package_broken`, `usage_misconfigured`,
`silent_exit`) stay as defined in `docs/COHORT.md`. Their matchers are
frozen for this pass.

**After the pass:** the 37 may be **characterized in aggregate** for the
limitation write-up. Characterization is not classification. Counts that
do not fire a pre-registered matcher remain `exec_failed`.

## R7-1 — `malformed` / `protocol_error`: server vs client (hand-probe)

Cohort dryrun6 holds **5** `malformed` and **10** `protocol_error` rows
(summary `malformed=46` includes tier-1 version-poll noise; only cohort
stdio rows are in scope here).

**Probe plan (fixed before looking at outcomes):** hand-probe all 5
`malformed` and 5 of 10 `protocol_error` (seed `20260806`). For each:

1. Re-spawn the published entrypoint the same way the crawler does
   (install + argv from registry; network denied).
2. Capture the first stdout line(s) after sending a normal MCP
   `initialize`.
3. Record whether the process is still alive when the bad bytes arrive.

**Decision criteria (bound now):**

| Finding | Verdict | Action |
|---------|---------|--------|
| Process speaks; bytes are not JSON, not JSON-RPC, JSON-RPC error, or initialize result lacking `protocolVersion` | **Theirs** — server violates the MCP/JSON-RPC contract | Apply **R7-2** |
| Bytes are valid MCP that our client rejects (framing we should accept, banner+JSON we should skip, SSE/stdio confusion, id/type coercion bug) | **Ours** — client bug | Fix client; do **not** exclude; re-observe affected ids |
| Process needs secrets/network/local config before speaking MCP | Already covered by existing exclusion classes | Reclassify via existing events; not a new class |
| Ambiguous after probe | Leave as `malformed` / `protocol_error` inside observable; note in residual | No new class |

No armchair classification. Probe first; then apply the row above.

## R7-2 — New exclusion class `protocol_broken` (conditional on R7-1)

**Activated only if** the hand-probe majority for each event is **Theirs**
under R7-1 (at least 3/5 malformed and 3/5 protocol_error, with no
confirmed client bug in the sample).

| Field | Value |
|-------|-------|
| Class | `protocol_broken` |
| Meaning | Stdio transport is up and the process is alive, but the server emits invalid JSON, invalid JSON-RPC, a JSON-RPC error on `initialize`/`tools/list`, or an initialize result without `protocolVersion` — permanently unobservable anonymously |
| Leaves observable denominator | Yes (same role as `package_broken`) |
| Maps from | cohort `malformed` + `protocol_error` that meet the probe standard |
| Does not absorb | Client bugs (R7-1 Ours); process-exit failures (`exec_failed` and spawn matchers); HTTP remotes |

**Justification:** A published server that completes process start and then
fails the protocol contract cannot be observed by any anonymous client.
That is the same scientific status as `package_broken`, reached by a
different route. It is not a sandbox artifact.

**If probes fail the activation threshold:** do not add the class. Keep
events as today; document probe outcomes; residual characterization
covers them if needed.

## R7-3 — What this pass will not do

- Will not open jail network to convert `network_required_at_runtime`.
- Will not supply credentials to convert `credentials_required`.
- Will not regenerate the cohort or move G5 thresholds.
- Will not iterate matchers after seeing catch counts.
- Will not treat `install_failed` / `entrypoint_unresolved` as permanent
  exclusions in this pass (still observable residual per `COHORT.md`).

## Execution order (locked)

1. Commit this file.
2. Hand-probe per R7-1; record outcomes.
3. If R7-2 activates, implement the class + summary/COHORT wiring; commit
   that implementation as a separate commit from this pre-registration.
4. Recompute G5a–G5d from dryrun6 cohort rows (reclassify in place where
   only denominator membership changes; full re-crawl only if R7-1 requires
   a client fix).
5. Inspect the 37 `exec_failed` **once**, for aggregate characterization
   only; write composition into `docs/COHORT.md`.
6. Report gates. Clock starts Monday either way.

## Outcomes (filled after execution)

- **R7-0:** held — no new `spawn_matchers-v1.txt` lines.
- **R7-1:** 5/5 malformed = Theirs; 3/5 protocol_error = Theirs; **2/5 =
  Ours (sandbox)** (quantrisk, apiosk: host `mcpclient` ok; jail
  `MODULE_NOT_FOUND` / dryrun6 `protocol_error`).
- **R7-2:** **not activated** (confirmed sandbox bugs in sample).
- **G5a–G5d:** unchanged from dryrun6 — G5a FAIL 7.7%, G5b PASS, G5c PASS
  307, G5d FAIL 76.2%. Residual composition written to `docs/COHORT.md`;
  limitation carried to `RESULTS.md`.
- **Clock:** week 1 starts Monday per stop rule (residual explained in
  aggregate; gates not cleared by invented classes).
