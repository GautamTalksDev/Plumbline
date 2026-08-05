# Plumbline results (kill-test evaluation)

**Evaluation date:** 2026-08-05 (UTC)  
**Pre-registration:** Kill-test thresholds are taken verbatim from the README at
CP-0 and are **not** adjusted in this document.  
**Taxonomy version evaluated:** `taxonomy-v1` (unchanged; no refit).  
**Canonicalization version:** `plumbline-canon-v1`.

---

## Verdict (one sentence each)

**KT-1: FAIL** — the pre-registered six-week observation series with signed
weekly checkpoints was not completed (0 of 6 weekly checkpoints at evaluation;
first crawl still in progress), so a ≥1% post-publication change rate over six
weeks is not established.

**KT-2: FAIL** — hand-labelled ground truth is empty (0 labelled rows), so the
classifier cannot be shown to label ≥80% of observed changes correctly as
cosmetic while catching ≥1 capability-relevant change.

These are negative results under self-imposed pre-registration. Thresholds were
not moved.

---

## 1. Method

| Item | Value |
|------|--------|
| Registry | Official MCP registry (`https://registry.modelcontextprotocol.io`) |
| Operator command | `plumb crawl` via `scripts/run-weekly-crawl.sh` |
| Intended cadence | Weekly (systemd timer `plumbline-crawl.timer`; optional GHA cron) |
| Pre-registered period | **Six consecutive weeks** with signed Merkle checkpoints |
| Period actually evaluated | **&lt; 1 day** — week-1 crawl started 2026-08-05 and was still running at evaluation; observation timestamps span ~0.06 hours |
| Canonicalization | `plumbline-canon-v1` (NFC → CRLF→LF → JCS → version prefix → SHA-256) |
| Taxonomy | `taxonomy-v1` (`SPEC-TAXONOMY.md` / `internal/classify`) — **not refitted** |
| Sandbox | Docker when available; else `unshare` + `plumbline-jail`; HTTP remotes allowlisted |
| Log | Append-only SQLite Merkle log (`~/.plumbline/crawl-log.sqlite`) |

### Crawl inventory at evaluation (interim, not a completed weekly series)

| Metric | Count |
|--------|------:|
| Distinct `server_id`s in log | 1575 |
| Servers marked completed in crawl state | 1574 |
| Total log entries | 8873 |
| `_crawl_status` rows | 1575 |
| Non-status tool rows | 7300 |
| Signed checkpoints stored | **0** (checkpoint is written at end of run) |
| Completed weekly runs with artifacts under `lab/runs/` | **0** finished summaries |
| Hand-labelled MEDIUM+ rows (`lab/labels/done/`) | **0** |

### Status event mix (single incomplete crawl; not failure of KT)

From `_crawl_status` payloads parsed at evaluation:

| Event | Count |
|-------|------:|
| `ok` | 425 |
| `transport_error` | 942 |
| `refused` | 180 |
| `protocol_error` | 12 |
| `malformed` | 7 |
| `sandbox_error` | 5 |
| `timeout` | 4 |

High `transport_error` / `refused` rates are operational coverage limits, not
taxonomy scores.

---

## 2. KT-1 — post-publication metadata change rate

**Pre-registered rule (README / CP-0):**  
If fewer than **1%** of crawled servers show any post-publication tool-metadata
change over **6 weeks**, this project is wrong and stops.

### Numbers available at evaluation

| Quantity | Value |
|----------|------:|
| N servers with ≥1 completed observation in state | 1574 |
| X servers with any tool-hash change across ≥2 observations | **0** |
| Interim X/N | **0.000%** |
| Observation window | ≪ 6 weeks (single incomplete crawl; one pass per server) |
| Signed weekly checkpoints in series | **0 / 6** |

**Confidence interval:** Not reported as a six-week result. An interim
Wilson interval on 0/1574 same-pass “changes” would be misleading: with one
observation per tool, post-publication drift is definitionally unobservable.
The pre-registered estimand is six-week longitudinal change, which was not
measured.

**KT-1: FAIL** (protocol and estimand not satisfied; ≥1% over six weeks not
shown).

---

## 3. KT-2 — classifier vs hand-labelled ground truth

**Pre-registered rule (README / CP-0):**  
If the classifier cannot label **≥80%** of observed changes correctly as
cosmetic while catching at least **one** capability-relevant change, it adds
nothing over `sha256sum` and this project stops.

### Numbers available at evaluation

| Quantity | Value |
|----------|------:|
| M observed changes with hand labels | **0** |
| K labelled correctly (cosmetic or otherwise per rubric) | **0** |
| Precision / recall per rule ID (R01–R09) | **undefined** (no labelled set) |
| Capability-relevant changes caught (hand-confirmed) | **0** |
| Taxonomy version | `taxonomy-v1` (no refit) |

Fixture suites under `testdata/taxonomy/` exercise `taxonomy-v1` determinism;
they are **not** a substitute for the pre-registered field ground-truth set.

**KT-2: FAIL** (empty ground truth; ≥80% bar not met; no confirmed
capability-relevant catch in field labels).

---

## 4. Notable cases

No capability-relevant field change was hand-confirmed in this evaluation
window.

**No MCP server maintainer is named in this document.** No notice period was
required because no third-party server is identified here as exhibiting
capability-relevant drift.

If a future evaluation names a server, the maintainer MUST be contacted first
with a documented notice period before public naming (see project security /
responsible-disclosure norms).

---

## 5. Limitations

1. **Registry coverage / reachability.** A large share of first-pass status
   events were `transport_error` or `refused`; many registry listings are not
   presently observable. Conclusions about the ecosystem rate of drift cannot
   be stronger than the reachable subset.
2. **Longitudinal depth.** Kill tests were pre-registered on **six weeks** of
   repeated observation. Evaluation occurred during the first incomplete crawl
   with **zero** signed weekly checkpoints.
3. **Hand labels.** The MEDIUM+ labelling pipeline (`lab/labels/`) was stood
   up but contains no completed reviews; KT-2 cannot pass without them.
4. **Rules not validated in the field.** Per-rule precision/recall for R01–R09
   on live drift is unknown. No claim is made that every taxonomy-v1 rule
   “worked” outside fixtures. If taxonomy must change, that is **v2 future
   work** — this document reports **v1’s honest result only**.
5. **Public log hosting.** CP-9’s “live on a real domain” deployment remained
   blocked in this environment; verification docs/CLI exist, but the public
   evidence surface was not a factor in these kill-test numbers.
6. **No threshold adjustment.** The 1% and 80% bars are unchanged from CP-0.

---

## Disposition

Under CP-11 / STOP guidance: **KT-1 has failed.** This repository should be
treated as a **negative result** for the pre-registered kill tests as
evaluated on 2026-08-05: the project did not produce the six-week labelled
evidence package required to continue under its own rules.

Continuing crawl ops after a failed pre-registered evaluation would be a
**new** study design and must not be reported as if it were the original
pre-registered test.

Taxonomy or protocol changes, if any, belong in a clearly versioned follow-on
(`taxonomy-v2`, new pre-registration) — not a silent refit of v1.
