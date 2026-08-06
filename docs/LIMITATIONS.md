# Limitations

Everything known to be incomplete, uncertain, or wrong about this study,
recorded at the point of cohort freeze rather than reconstructed afterwards.

This file is maintained during the study, not written at the end. Items are
added when discovered. Nothing is removed; items that get resolved are marked
resolved with a date.

**Status at freeze:** cohort frozen at 479 members. Study period runs six weekly
crawls. Kill tests KT-1 and KT-2 are evaluated only at CP-11, against
`README.md` thresholds that do not move.

---

## 1. Coverage: what this study does not see

### 1.1 The remotes population is excluded entirely

The registry contains **20,249** servers, splitting as:

| Slice | Count |
|---|---:|
| Packages only (stdio: npm, PyPI, OCI, MCPB) | 9,675 |
| Remotes only (HTTP) | 9,162 |
| Both | 1,060 |
| Neither | 352 |

**v1 covers the packages population only.** Servers reachable only over HTTP are
excluded and are not in any denominator.

The reason is empirical, not arbitrary. A partial crawl of 1,912 HTTP remotes
produced a 70% failure rate, of which **55% were HTTP 401** — the hosted,
vendor-operated half of the ecosystem authenticates by design and will never
return tool metadata to an unauthenticated client. Since we do not circumvent
access controls (`ETHICS.md` §4), those servers are permanently unobservable to
us. Including them would dilute any drift rate with servers we never actually
observed.

**Consequence:** findings generalise to stdio/package MCP servers. They do not
generalise to hosted OAuth-gated servers, which are a large and growing share of
the ecosystem and skew toward commercial vendors.

### 1.2 OCI and MCPB packages are excluded

Cohort eligibility is npm and PyPI only. OCI and MCPB packages cannot be
installed by the tier-2 path. The cohort was regenerated on this basis before
the freeze (537 → 479 members).

### 1.3 The registry is not the ecosystem

The official MCP registry is metadata-only and does not index every MCP server
in existence. Private and enterprise servers, and servers published only to
third-party directories, are outside the frame entirely.

---

## 2. Observability: what we could not observe within the cohort

At freeze (dry run 6, 479 cohort members):

| Class | n | Meaning |
|---|---:|---|
| `package_broken` | 60 | Import error, syntax error, missing module — fails identically on the host |
| `credentials_required` | 57 | Exits citing a missing API key, token, or required env var |
| `runtime_incompatible` | 25 | Node/Python engine or version mismatch |
| `usage_misconfigured` | 13 | Prints CLI usage and exits; registry entry does not match the package |
| `silent_exit` | 10 | Exits 0, no stderr, never speaks MCP |
| `network_required_at_runtime` | 7 | Requires outbound network at startup; denied by the sandbox |
| **Total excluded** | **172** | |
| **Observable population** | **307** | 64% of the cohort |

**KT-1's denominator is 307, not 479.** This is stated wherever a drift rate is
reported.

Two of these classes deserve separate comment.

- **`network_required_at_runtime` is our constraint, not theirs.** These servers
  work; our deny-all sandbox prevents them from starting. We do not open the
  network to accommodate them, because doing so would give a stranger's process
  live egress from our host. This is a deliberate trade that costs us 7 servers.
- **`silent_exit` is a finding, not just an exclusion.** Ten servers exit
  successfully having accomplished nothing and reported nothing.

### 2.1 Residual unexplained failures exceed the pre-registered cap

**37 of 479 (7.7%)** cohort failures remain unclassified against a 5% cap. They
are characterised in aggregate only — 19 opaque, 7 credential/config-like that
the matchers did not catch, 6 empty-stderr exit 2, 3 crashes, 1 native, 1 empty
exit 1 — and were **not** reduced by inventing exclusion classes to fit them.
Round-7 exclusion rules were pre-registered in `docs/ROUND7-PREREG.md` before
the residual was inspected.

### 2.2 Contamination of `package_broken` is bounded, not measured

Ten `package_broken` members (seed `20260806`) were re-run on the host outside
the sandbox. **0 of 10** succeeded, so none were misclassified sandbox failures.

By the rule of three, zero hits in ten draws is consistent with a true
contamination rate **up to roughly 25% at 95% confidence**. This is a loose
bound on the largest exclusion class and is stated as such rather than reported
as "0%".

### 2.3 A known sandbox staging bug is unfixed

Hand-probing of `protocol_error` found **2 of 10** cases that succeed on the host
but fail in the jail with `MODULE_NOT_FOUND` — something about how the install
tree is staged does not survive `pivot_root`. These were **not** promoted into an
exclusion class, because doing so would have laundered our own fault into a
finding. The bug is real, unfixed at freeze, and may affect an unknown fraction
of other failure classes. §2.2 bounds it for `package_broken` only.

---

## 3. Gate status at freeze

Gate 5 was a pre-freeze engineering gate, distinct from the kill tests.

| Gate | Requirement | Result |
|---|---|---|
| G5a | Unexplained ≤5% of cohort | **FAIL** — 7.7% (37/479) |
| G5b | No terminal state with both empty stderr and missing exit code | **PASS** — 0 |
| G5c | Observable population ≥200 | **PASS** — 307 |
| G5d | `ok` ≥90% of observable | **FAIL** — 76.2% (234/307) |

Two of four gates failed. The study proceeded anyway, deliberately: the gate
existed to ensure the denominator could be explained, and at 93% explained it
substantially is. Further delay would have traded evidence-collection weeks for
diminishing returns. The failures are carried forward here rather than resolved
by moving the thresholds.

---

## 4. Method constraints

- **Canonicalisation is versioned** (`plumbline-canon-v1`). A future protocol
  change that adds a field will change every hash at once; the negotiated
  protocol version is recorded alongside every entry so "the protocol changed"
  is distinguishable from "this tool changed".
- **Taxonomy v1 is frozen for the study.** Rules are not refit to observed data
  and then reported as pre-registered. If v1 rules prove wrong, v1's honest
  result is reported and v2 is future work.
- **Event taxonomy v2** splits failure classes that v1 collapsed into
  `transport_error`. Pre-v2 rows are excluded from per-class analysis; there is
  no backfill, because the log is append-only.
- **Version polling detects candidate drift, not all drift.** Registry
  `server.json` is immutable once published, so drift for stdio servers occurs
  upstream at npm/PyPI. Tier 1 polls all packages; tier 2 spawns on version
  change *plus* the full fixed cohort every week, which is what catches
  description changes shipped without a version bump.
- **Ground truth is hand-labelled by one person** — the author. There is no
  second labeller and no inter-rater agreement statistic.

---

## 5. Engineering debts at freeze

Recorded in `docs/PUBLIC-FLIP.md`; repeated here because they bear on how much
weight the results can carry.

| Debt | Impact | Why |
|---|---|---|
| **No CI** | Cross-platform correctness is compile-checked only. `go vet` and cross-builds for darwin and windows run in a local pre-push hook; tests actually execute on Linux only. A Windows-only runtime bug would not be caught. | GitHub Actions is blocked by an account budget that cannot be removed without a payment method. |
| **No signed releases** | Sigstore keyless signing requires CI OIDC. Until restored, no cryptographically verifiable binary can be published — which weakens the "verifiability instead of trust" answer to the single-maintainer problem. | Same cause. |
| **Local hooks are bypassable** | `git push --no-verify` skips all checks. Hooks are developer feedback, not enforcement. | Inherent to hooks. |
| **Crawl host** | The machine that executes 479 third-party packages weekly should be disposable and hold nothing. The signing key has been moved offline; full host separation is not complete. | In progress. |

---

## 6. Things that would change the conclusions

Stated in advance so that finding them later is not a surprise:

- If the §2.3 jail staging bug affects a large share of `package_broken`, the
  observable population is larger than 307 and the exclusion taxonomy is wrong.
- If drift concentrates in the excluded OAuth-gated remotes population, this
  study measures the quiet half of the ecosystem and understates the threat.
- If a maintainer opts out mid-study, the cohort loses a member and the
  denominator changes; departures are recorded, not replaced.
- If the MCP specification changes during the six weeks, all hashes shift at
  once. The recorded protocol version is what distinguishes that case; if it
  proves insufficient, the affected weeks are reported as such.
