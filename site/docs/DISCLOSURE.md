# Disclosure Policy

Two distinct things are governed here, and conflating them causes harm:

1. **Vulnerabilities reported to us**, in Plumbline itself.
2. **Findings we report about others** — servers whose tool metadata changed.

The second is the unusual one and the one most likely to hurt someone. It is
covered first.

---

## Part 1 — Findings about observed servers

### The governing rule

**We publish observations. We never publish motives.**

"Server X's tool description changed on 2026-08-14; here is the canonical diff,
the matched rule IDs, and the log index" is a fact provable from a signed,
append-only log that anyone can verify independently.

"Server X attempted to poison agents" is a claim about a person's intent that we
cannot prove, do not need to make, and will not make. Readers can draw their own
conclusions from the evidence; that is what the evidence is for.

Most maintainers whose descriptions change did nothing wrong. Fixing a typo,
clarifying a parameter, and adding a feature all produce drift. A tool that
names servers without severity context and without a right of reply makes
enemies of the people whose cooperation the project depends on.

### Before any server is named publicly

In order, without exception:

1. **The classifier runs first.** No server is named on the basis of "the hash
   changed". A named finding must carry a severity and the specific rule IDs
   that fired, per `SPEC-TAXONOMY.md`.
2. **The maintainer is notified privately**, via the contact route published
   with the package or repository.
3. **A 90-day notice period** runs from that notification before publication.
   This matches the disclosure timeline in `SECURITY.md` and is the standard
   coordinated-disclosure window.
4. **A right of reply is offered.** If the maintainer responds, their
   explanation is published alongside the finding, in their words, unedited
   except for length. If they dispute the classification, the dispute is
   published.
5. **Notification is documented** — date sent, route, and any response — and
   that record is part of the published methodology.

If a maintainer cannot be reached after reasonable effort, that fact is stated
and the notice period still runs in full.

### What we withhold

There are cases where the responsible action is to publish less.

- **Aggregate over specific.** If a finding informs the public no more when
  attached to a named server than when reported in aggregate, it is reported in
  aggregate. Precedent exists: coordinated-disclosure researchers have withheld
  per-organisation datasets specifically to avoid harm to affected parties while
  still publishing their findings.
- **Live exploitation detail.** If a specific observed change constitutes a
  working attack against current clients, the mechanism is withheld until the
  maintainer or the affected clients have remediated, regardless of whether 90
  days have elapsed.
- **Anything that harms more than it informs.** Where the balance is unclear, we
  publish less and say that we did so, rather than publishing more and
  discovering afterwards that it was wrong.

Withholding is recorded — the reader is told that something was withheld and
why, even when they are not told what.

### What we will not do

- Name a server before the notice period ends.
- Characterise a maintainer's intent, competence, or trustworthiness.
- Publish maintainer names, email addresses, or account identifiers. Findings
  identify **software**, not people (`ETHICS.md` §1).
- Publish the full corpus of collected tool descriptions. We publish hashes,
  structured diffs, rule IDs, and short excerpts sufficient to show a finding.
  Republishing everyone's prose wholesale serves no research purpose and would
  make us a redistribution vector for any malicious payload we found.
- Reclassify a finding upward after publication without publishing the
  correction with equal prominence.

### Corrections

If we publish something wrong, the correction is published in the same place,
with the same prominence, and the original is annotated rather than deleted. The
log is append-only; so is the record of what we said about it.

---

## Part 2 — Vulnerabilities in Plumbline

Plumbline is a security tool. It parses attacker-controlled JSON, spawns
untrusted subprocesses, and operates a public endpoint. It will have
vulnerabilities.

### How to report

Use **GitHub private vulnerability reporting** on this repository, or the
contact in `SECURITY.md`. Please do not open a public issue for a security
problem.

Useful reports include: what you did, what happened, what you expected, and
whether you believe it is exploitable in the default configuration.

### What to expect

| Stage | Target |
|---|---|
| Acknowledgement | 7 days |
| Initial assessment | 14 days |
| Coordinated disclosure window | 90 days from report |

These are targets from a single maintainer with no service-level agreement, not
guarantees. If a report is not acknowledged in 7 days, it has been missed rather
than ignored; please follow up.

Credit is given to reporters who want it and withheld from those who do not.

### Safe harbour, honestly stated

We will not pursue legal action against anyone who reports a vulnerability in
good faith, does not access or modify data belonging to others, does not degrade
the service for others, and gives us reasonable time to respond before public
disclosure.

That commitment is ours to give and we give it plainly. It is not a legal safe
harbour — most jurisdictions do not provide one for security researchers, and
we cannot bind third parties. It reflects how we will behave.

---

## Part 3 — Opting out of the crawl

Any maintainer may have their server excluded from the Plumbline crawl, for any
reason, without justification.

The mechanism is documented in `docs/CRAWLING.md` and `docs/opt-out.txt`, and
requests are honoured **within 24 hours**.

Opting out during a study period is recorded as a departure in the methodology —
the cohort is frozen, so the member is marked departed rather than replaced.
No prior observations are deleted, because the log is append-only and
retroactively editable history would defeat the purpose of the project. No
further observations are made.

If the existence of prior observations is itself the concern, contact us and
we will explain precisely what is recorded (public tool metadata only; no tool
call contents, no user data, no personal information) and discuss what can be
done within the constraints of an append-only log.
