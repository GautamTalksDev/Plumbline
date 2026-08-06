# Control cohort (plumbline-cohort-v1)

This is the scientific control for Plumbline weekly crawls. Cohort members are
spawned **every week** regardless of version change, to catch description-only
drift that a version poll cannot see.

## Definition

A server id is in the cohort iff **all** of:

1. It is in the **packages population** (non-empty `packages[]`).
2. Its primary package registry type is **npm or pypi** (installable).
3. The hash rule holds:

```
uint64(first 8 bytes of SHA-256("plumbline-cohort-v1|" + server_id)) mod 10000 < 500
```

- Cohort version string: `plumbline-cohort-v1`
- Modulus: 10000
- Threshold: 500 (~5% of the eligible installable packages population)
- Selection depends on **nothing but** the server id and the cohort version
  string — no randomness, timestamps, or local state — once eligibility is met.

## Why npm/pypi only

Tier-2 spawn performs a real install (`npm install --ignore-scripts` /
`pip install --target`) then executes the resolved entrypoint with network
denied. **oci** and **mcpb** packages have no install path in this crawler, so
they cannot be spawned. Including them would force a tier-1 class
(`unpollable`) into the spawn phase. They are therefore excluded from cohort
eligibility at generation time. Remotes-only servers remain out of scope for v1.

## Freeze policy

The member list is frozen for the study. Do not add or remove members
mid-study. Servers that leave the registry are recorded as `departed`,
not replaced. Regenerating after week 1 starts requires a new cohort version.

## Snapshot (generated)

- Packages population size at generation: **10738**
- Installable (npm/pypi) eligible pool: **9834**
- Cohort members: **479**
- Member list file: [`cohort-v1-members.txt`](cohort-v1-members.txt)

Regenerate (operators only; do not commit a mid-study regen without a new
cohort version):

```bash
go run ./scripts/gen-cohort.go
```

Anyone can recompute membership for an arbitrary id with the definition
above, or by calling `crawler.InControlCohort(id)` ∩
`crawler.CohortEligible(server)` in Go.

## Observability exclusions (gate 5)

Not every cohort member can be observed anonymously inside a network-denied
jail. Gate 5 requires every failure to be *explained*. The following event
classes are **demonstrated-permanent exclusions** and leave the observable
denominator:

| Class | Meaning |
|-------|---------|
| `credentials_required` | Needs API keys / secrets / local login we will never supply |
| `network_required_at_runtime` | Performs DNS/HTTP/download at startup; deny-all correctly blocks |
| `runtime_incompatible` | Needs a newer/different Node or Python than the jail provides |
| `package_broken` | Published package fails to import or locate its own modules |
| `usage_misconfigured` | Still prints CLI usage after registry `packageArguments` / fixed `runtimeArguments` are applied — registry entry wrong or entrypoint incomplete |
| `silent_exit` | Exits 0 with empty stderr without speaking MCP — success with no work |

**Network is never opened** to convert `network_required_at_runtime` into
`ok`. That count is itself a finding: share of stdio servers that require
outbound network before they will speak MCP.

### Gate 5 (G5a–G5d)

- **G5a:** unexplained `exec_failed` ≤ 5% of the cohort.
- **G5b:** every non-`ok`, non-`timeout` observation has stderr preview and/or exit code (no blind rows).
- **G5c:** observable population (cohort − exclusion classes above) ≥ 200; every exclusion class named and counted here.
- **G5d:** `ok` ≥ 90% of observable.

`install_failed` and `entrypoint_unresolved` remain classified failures but
are not in the permanent-exclusion set above; they count against the
observable residual until separately justified.

## Round 7 freeze (2026-08-06) — dryrun6 baseline

Pre-registration: [`ROUND7-PREREG.md`](ROUND7-PREREG.md) (commit before probes).
Stop rule: one classifier pass, then week-1 clock starts Monday regardless.

### G5 scorecard (dryrun6; Round 7 does not re-crawl)

| Gate | Result | Detail |
|------|--------|--------|
| **G5a** unexplained ≤5% | **FAIL** | **37 / 479 = 7.7%** (cap ≈24) |
| **G5b** no blind process rows | **PASS** | **0** |
| **G5c** observable ≥200 | **PASS** | **307** |
| **G5d** ok ≥90% of observable | **FAIL** | **234 / 307 = 76.2%** |

Exclusion counts (leave denominator): credentials_required 57,
package_broken 60, runtime_incompatible 25, usage_misconfigured 13,
silent_exit 10, network_required_at_runtime 7 — **172** excluded.
Observable **307**; raw ok **234**.

### R7-1 hand-probe (`malformed` / `protocol_error`)

Cohort holds **5** `malformed` and **10** `protocol_error` (summary
`malformed=46` includes tier-1 version-poll noise — out of scope).

Probed all 5 `malformed` and 5/10 `protocol_error` (seed `20260806`):

| Verdict | n | Meaning |
|---------|--:|---------|
| **Theirs** | 8 | Stdio contract broken: CLI/HTTP banners on stdout, non-JSON banner before JSON-RPC, bare initialize result (no `jsonrpc`/`id`), or `tools/list` JSON-RPC −32603 |
| **Ours (sandbox)** | 2 | Host `mcpclient` completes initialize+`tools/list`; jail spawn yields `MODULE_NOT_FOUND` / dryrun6 `protocol_error` |

**R7-2 `protocol_broken` not activated** — activation required majority
Theirs on both events *and* zero confirmed client/sandbox bugs. The two
host-ok / jail-fail servers are ours; promoting the whole pile would launder
sandbox faults into exclusions.

No new `spawn_matchers-v1.txt` substrings (R7-0). Matchers frozen for this pass.

### Residual 37 `exec_failed` — aggregate composition

Characterization only (not new exclusion classes). One pass over dryrun6
stderr tails:

| n | Composition |
|--:|-------------|
| 19 | Opaque / unstructured (wrong CLI option text on stderr, `ENOENT`, circular dumps, other) |
| 7 | Credential/config-*like* phrasing that existing matchers did not fire on |
| 6 | Empty stderr, exit 2 (likely stdout-only CLI failure) |
| 3 | Process crash (`TypeError` / Node error) |
| 1 | Native/binary-ish failure |
| 1 | Empty stderr, exit 1 |

Plus observable non-ok that are named but not permanent exclusions:
`install_failed` 11, `entrypoint_unresolved` 10, `malformed` 5,
`protocol_error` 10.

**Explained in aggregate:** the unexplained pile is not a single crawler bug.
It is a mix of matcher-miss permanent modes, stdout/CLI misfires already
visible in other classes, crashes, and a long opaque tail — plus two
confirmed jail/sandbox faults inside `protocol_error`. Perfect 0/479 is not
worth another classifier week.

### Carry-forward limitation

Week-1 starts with this residual on the books. Report in `RESULTS.md`:
unexplained `exec_failed` = **37 (7.7%)**; ok/observable = **76.2%**;
Round-7 did not clear G5a/G5d and did not invent exclusion classes to do so.

### `package_broken` contamination check (2026-08-06, pre-Monday)

R7-1 found 2/5 probed `protocol_error` rows were jail faults (host ok).
That raised the question whether the largest exclusion class
(`package_broken`, n=60) is similarly contaminated.

**Measurement only** (seed `20260806`, same install+host-`mcpclient` path as
R7-1; no reclassification, no jail fix): **10/10 random `package_broken`
members also fail on the host** (0/10 host-ok). Dominant mode: published
packages importing `mcp.server.fastmcp` / modules absent from their install
closure. npm cases failed on host Node v18.20.8 with the same class of load
error as in-jail.

**Contamination rate in this sample: 0/10.** `package_broken` is treated as
sound for the week-1 denominator freeze. The R7-1 jail staging bug remains
real for some `protocol_error` rows but does not, on this sample, leak into
`package_broken` at detectable rate.

