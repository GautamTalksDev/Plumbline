# Plumbline crawling policy

This document describes what the Plumbline registry crawler (`plumb crawl`)
collects, what it never collects, and how to opt out.

The crawler is an **operator command** (Linux only). It is not part of
`plumb check`. End users running `plumb check` never trigger a crawl.

## Scope (v1)

**Packages population only.** A server is in scope iff its registry listing
has a non-empty `packages[]` array (~10 738 of ~20 254 listings at study
start). Servers with only `remotes[]` (~9 164) are **excluded** from the
weekly crawl. The remote observation code path remains in the tree but is
gated behind `--include-remotes` (off by default).

Rationale: the MCP registry treats `server.json` as immutable, so drift
cannot originate there. For stdio/package servers the drift channel is the
upstream package registry — a user's client runs `npx <identifier>` which
resolves to npm-latest, not the registry-pinned version. A version poll
detects candidate drift without spawning.

## Two-tier crawl

### Tier 1 — version poll (weekly, full packages population)

For every in-scope server:

- npm → GET package metadata from the npm registry
- pypi → GET the PyPI JSON API
- oci / mcpb / other → record event `unpollable` (no upstream poll)

Each observation records: identifier, registry type, registry-pinned
version, upstream-latest version, whether they differ, upstream publish
timestamp, poll timestamp. Appended to the Merkle log. Concurrency **8**,
polite delay between registry pages, honest User-Agent. **No sandbox** —
metadata fetch only.

### Tier 2 — spawn and hash (weekly, targeted)

Spawn, `tools/list`, canonicalize and hash for the union of:

1. every server whose upstream-latest version **changed** since the previous
   poll, and
2. the fixed **control cohort** (see [`COHORT.md`](COHORT.md)).

Package fetch allows egress to the package-registry host only during a
bounded fetch phase; the existing CP-8 sandbox then runs with default-deny
network. Fetch-phase failures are recorded as `fetch_error`, distinct from
spawn failures.

The cohort is spawned **every week** regardless of version change.

## What we collect

For each in-scope server the crawler records:

- Server identity (registry name / id)
- Tier-1 version poll fields (above)
- On tier-2 spawn: public **tool metadata** from MCP `tools/list`
- Observation outcome (typed failure / success — see Event taxonomy)
- Observed HTTP status code when applicable (nullable)
- A short diagnostic string from a fixed set (never server-supplied text)
- Protocol version advertised during initialize (tier 2)
- A signed Merkle-log checkpoint at the end of each **complete** weekly run

Canonical hashes of tool metadata are appended to an append-only Merkle log.

## Completeness and the six-week series

A run is **complete** only if tier 1 finished the full packages population
and tier 2 finished the whole control cohort (plus any version-change
spawns queued with it). Partial / killed runs write `summary.json` with
`complete: false` and `counts_toward_six_week_series: false` — they are
**not** counted toward the six-week kill-test series. Resume is
crash-safe: a persisted cursor covers both tiers without duplicating or
losing observations.

`summary.json` is written atomically (temp file, fsync, rename).

## Event taxonomy

Each status row records `events_version`.

### events-v1 (historical)

Pre-reclassification crawls collapsed many distinct failures into a single
`transport_error` bucket (alongside `ok`, `timeout`, `refused`, `malformed`,
`too_large`, `protocol_error`, `sandbox_error`, and `opt_out`). Those rows
remain in the append-only log unchanged. **Do not backfill.** Exclude
`transport_error` / events-v1 rows from per-class failure analysis.

### events-v2 (current)

New observations use `events_version: "events-v2"`. `transport_error` is no
longer emitted. Mutually exclusive failure classes:

| Event | Meaning |
|-------|---------|
| `ok` | Initialize + tools/list succeeded (tier 2) |
| `version_poll` | Tier-1 upstream version poll succeeded |
| `unpollable` | Package registry type has no version API (oci/mcpb/…) |
| `fetch_error` | Bounded package-fetch phase failed |
| `departed` | Frozen cohort member no longer in the registry |
| `auth_required` | HTTP 401 or 403 |
| `endpoint_404` | HTTP 404, or 400 with a body indicating no such route |
| `dns_failure` | Name resolution failed |
| `tls_failure` | TLS handshake or certificate validation failed |
| `http_error` | Any other non-2xx HTTP status (status recorded) |
| `protocol_error` | Transport succeeded; MCP handshake did not |
| `malformed` | Valid transport; invalid JSON-RPC |
| `timeout` | Deadline exceeded |
| `sandbox_error` | Containment layer failed; target never reached |
| `refused` | Connection refused, or no dialable target in the spec |
| `too_large` | Response exceeded size cap |
| `opt_out` | Server opted out; not contacted |

Streamable HTTP probes (when `--include-remotes`) **POST** initialize first.

## What we never collect

- **No tool call contents.** The crawler never invokes tools (`tools/call`).
- **No user data, prompts, files, credentials, or session tokens.**
- **No private server installs.** Only servers published in the public registry
  (or explicitly configured operator targets in tests) are contacted.
- **No host filesystem mounts into the sandbox.** Each probe runs in an
  ephemeral jail/container that is destroyed afterward.

## How we crawl

- Pagination over the registry API with a **persisted cursor** so a crash resumes
  rather than restarting (covers both tiers).
- Tier 1: at most **8 concurrent** version polls; polite delay between registry
  page fetches.
- Tier 2: at most **4 concurrent** sandboxed spawns.
- User-Agent:
  `PlumblineCrawler/0.1 (+https://github.com/GautamTalksDev/Plumbline/blob/main/docs/CRAWLING.md)`
- Local/package execution runs only inside a sandbox: non-root, read-only
  rootfs, no host path mounts, default-deny network after fetch, memory
  ≤512 MiB, CPU capped, 60s wall clock then SIGKILL, ephemeral.
- Stdio/package probes use Docker/Podman when the daemon is available, otherwise
  `unshare` + `plumbline-jail` (pivot_root). Install the helper with
  `go install ./cmd/plumbline-jail` (or set `PLUMBLINE_JAIL_HELPER`).
- Weekly runs are driven by **systemd** (`ops/systemd/plumbline-crawl.timer`),
  not `nohup`.

## Opt out (honoured within 24 hours)

You can opt a published server out of crawling in either of these ways:

### 1. Registry metadata (preferred)

Publish registry `_meta` on the server listing:

```json
{
  "_meta": {
    "plumbline.org/opt-out": true
  }
}
```

The crawler treats this as an immediate opt-out for that listing (recorded as
event type `opt_out`, with **no connection** attempted).

### 2. Project opt-out list

Email the maintainer (see Contact) with the exact registry server name
(for example `vendor/server`). We add it to [`opt-out.txt`](opt-out.txt) in this
repository. Operators running `plumb crawl` load that file by default.

**SLA:** opt-out requests are honoured within **24 hours** of acknowledgment
(merged to the default branch and picked up by the next crawl run). Until the
list updates, registry `_meta` opt-out is the fastest path.

Opted-out servers still produce a single log row with event type `opt_out` so
the public log remains complete; they are never contacted.

## Contact

- Opt-out / crawling questions: open a GitHub issue with the subject prefix
  `[crawl-opt-out]` or email the maintainer listed on the GitHub profile for
  [GautamTalksDev/Plumbline](https://github.com/GautamTalksDev/Plumbline).
- Security issues: see [SECURITY.md](../SECURITY.md) (private reporting).

## Operator safety gate

`plumb crawl` refuses to start against a live configuration unless:

```bash
export PLUMBLINE_CRAWL_I_UNDERSTAND=1
```

Do not set this until containment tests pass (`go test ./internal/crawler/ -run Containment`).
Never run stranger-executed code outside the sandbox. Never point an unsandboxed
client at untrusted stdio packages on your laptop.
