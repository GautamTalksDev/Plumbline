# Ethics

This document states how the Plumbline crawl is designed and operated, and
why. It is written to be read by a maintainer whose server we observe, by a
reviewer, and by anyone deciding whether to trust the log.

Plumbline is designed and conducted in accordance with the principles of the
**Menlo Report** (Dittrich & Kenneally, DHS, 2012), the ethical framework for
information and communication technology research and network measurement. The
project has no institutional review board; declaring alignment with Menlo and
addressing each principle explicitly is the substitute, and it is a weaker one.
This document is the accountability mechanism.

The four principles are addressed in turn.

---

## 1. Respect for persons

**The unit of analysis is a published software package, not a person.**

Plumbline observes the tool metadata that an MCP server advertises: tool names,
descriptions, input and output schemas, annotations, and any `_meta` field. That
metadata is published by its authors to a public registry for the express
purpose of being read by clients. We are a client.

- No human subjects are enrolled, observed, or contacted as research subjects.
- We collect **no personally identifiable information**. We do not collect or
  store maintainer names, email addresses, or account identifiers, even where
  package registries expose them.
- We record server identifiers and package identifiers, which are project
  names, not people.
- We never observe or record the *contents* of any tool call, any user data, or
  any traffic between a third party and a server.

Where a maintainer is identified in a future publication, that identification
is governed by `DISCLOSURE.md`, not by this document.

**Note on jurisdiction.** Some maintainers are in the EU, and package-registry
metadata can constitute personal data under the GDPR. Our answer is
minimisation: we do not collect the fields that would make this question live.

---

## 2. Beneficence — weighing benefit against harm

### The benefit

No public, independently verifiable record exists of what MCP tools have
claimed they could do over time. The only longitudinal data on MCP tool-metadata
drift is held privately by a commercial security vendor and was obtained by
having users submit their own tool surfaces. Plumbline is the public
alternative: a log anyone can audit, produced without asking anyone to install
anything or send us their data.

If the drift threat is real, evidence of it is a public good. If it is not, a
credible negative result is also a public good, and the project is
pre-registered to publish that outcome rather than bury it (see
`docs/LIMITATIONS.md` and the kill tests in `README.md`).

### The harms, and how each is bounded

**Harm to the servers we contact.** We are executing and connecting to software
written by thousands of strangers. Load and disruption are real risks.

- Maximum 4 concurrent connections, with a polite delay between requests.
- Hard deadlines: 30s connection, 10s per request, 60s wall clock per server.
- Hard size caps: 8 MiB per response; 1 MiB per tool object.
- An honest `User-Agent` identifying the project, with a link to `docs/CRAWLING.md`.
- Tier 1 is metadata-only version polling against npm and PyPI, using those
  registries exactly as an ordinary consumer would.

**Harm to third parties via our own infrastructure.** A hostile package could
attempt to use our host to attack someone else.

- Every spawn runs inside an isolated sandbox: non-root, read-only rootfs, no
  mounted host paths, memory and CPU caps, ephemeral and destroyed after each
  server.
- **Network egress is denied by default.** The only exception is a bounded
  package-fetch phase against the package registry host; network is dropped to
  deny-all *before* the server process starts.
- `npm install --ignore-scripts` and `pip install --no-input`: package
  lifecycle scripts are **never** executed. Lifecycle scripts run arbitrary code
  with full privileges during the one phase in which the network is open, which
  is the worst possible moment. Packages that consequently fail are recorded as
  a data point, not worked around.
- Containment is tested against a deliberately malicious server that attempts to
  write files, spawn processes, and reach an external host, before any live
  crawl.

**Harm to ourselves, and thereby to the log's integrity.** The signing key for
the transparency log is held offline, on a machine that never executes foreign
code. The crawl host holds no credentials.

**Harm to maintainers from publication.** Addressed in `DISCLOSURE.md`. The
governing rule: we publish observations, never motives.

---

## 3. Justice — fairness in who is studied

The control cohort is selected by a published deterministic function of the
server identifier:

```
include server_id iff
  uint64(first 8 bytes of SHA-256("plumbline-cohort-v1|" + server_id))
  mod 10000 < 500
```

Selection depends on nothing but the identifier and the cohort version string.
No randomness, no timestamps, no local state, no human judgment about which
servers are interesting. **Anyone can regenerate the exact cohort from public
data** (`docs/COHORT.md`, `scripts/gen-cohort.go`).

This matters because the alternative — studying whichever servers are large,
convenient, or already suspected — would let the researcher choose the finding.
The cohort was frozen before the study period began and is not modified
mid-study. Servers that leave the registry are recorded as departed, not
replaced.

Eligibility is restricted to npm and PyPI packages. That restriction, and the
populations it excludes, are documented in `docs/LIMITATIONS.md` rather than left
implicit.

---

## 4. Respect for law and public interest

### Legal due diligence

- **Public data only.** Every observation comes from a public registry listing
  or a server that answered an unauthenticated connection.
- **We never circumvent an access control.** A server that returns 401 or 403 is
  recorded as `auth_required` and left alone. We do not create trial accounts,
  complete OAuth flows, or use credentials of any kind to reach a server that
  refused us. This is a standing rule, not a case-by-case judgment: the legal
  distinction between reading public data and exceeding authorised access turns
  on exactly this line.
- **robots.txt and stated wishes are respected**, and a documented opt-out is
  honoured within 24 hours (`docs/CRAWLING.md`, `docs/opt-out.txt`).
- Package registries are used as an ordinary consumer of those registries.

We are not lawyers and this is not legal advice. It is a description of the
constraints the system enforces.

### Transparency of method and limitations

Menlo's transparency principle requires openly disclosing methods, limitations,
and dual-use implications. Concretely:

- The canonicalisation algorithm is versioned (`plumbline-canon-v1`) and pinned
  into every log record, so a future change cannot silently invalidate history.
- The severity taxonomy is a published specification (`SPEC-TAXONOMY.md`,
  `taxonomy-v1`) implementable without reading our code, with the imperative
  verb list held as reviewable data rather than code.
- The log is append-only with signed tree heads, and `plumb verify-log` works
  against **any** conforming endpoint, not just ours. We are not a trusted party
  in our own system.
- Kill tests were written into `README.md` before any code existed, and their
  thresholds are not adjusted after seeing data.
- `docs/LIMITATIONS.md` records everything known to be wrong or unmeasured, including
  results that reflect badly on the project.

### Dual use

This is the unresolved tension and we state it rather than pretend otherwise.

The severity taxonomy is a detector. The same published rule list tells an
attacker which mutations fall below our thresholds and which fields we do not
inspect. Publishing it is nonetheless the right call: a detection rule that
only works while secret is not a rule, it is an obstacle, and an unpublishable
taxonomy cannot be adopted, audited, or improved by anyone else. The
alternative — a private scoring system users must take on faith — is the thing
this project exists as an alternative to.

We accept the trade and name it. Where a specific finding would provide more
uplift to an attacker than defence to anyone else, `DISCLOSURE.md` governs what
is withheld.

---

## Contact

Questions, objections, or opt-out requests: see `SECURITY.md` and
`docs/CRAWLING.md`. Opt-out requests are honoured within 24 hours and require no
justification.
