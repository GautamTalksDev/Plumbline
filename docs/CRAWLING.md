# Plumbline crawling policy

This document describes what the Plumbline registry crawler (`plumb crawl`)
collects, what it never collects, and how to opt out.

The crawler is an **operator command** (Linux only). It is not part of
`plumb check`. End users running `plumb check` never trigger a crawl.

## What we collect

For each server listed in the [official MCP registry](https://registry.modelcontextprotocol.io),
the crawler records:

- Server identity (registry name / id)
- Public **tool metadata** returned by MCP `tools/list` (tool name, description,
  input schema, annotations, and other fields present on the tool object)
- Observation outcome (success or a typed failure — see Event taxonomy below)
- Observed HTTP status code when applicable (nullable)
- A short diagnostic string from a fixed set (never server-supplied text)
- Protocol version advertised during initialize
- A signed Merkle-log checkpoint at the end of each crawl run

Canonical hashes of tool metadata are appended to an append-only Merkle log.

## Event taxonomy

Each `_crawl_status` row records `events_version`.

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
| `ok` | Initialize + tools/list succeeded |
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

Streamable HTTP probes **POST** the initialize request first. An empty SSE
response to GET alone is not treated as unreachable — some servers only
respond on POST.

## What we never collect

- **No tool call contents.** The crawler never invokes tools (`tools/call`).
- **No user data, prompts, files, credentials, or session tokens.**
- **No private server installs.** Only servers published in the public registry
  (or explicitly configured operator targets in tests) are contacted.
- **No host filesystem mounts into the sandbox.** Each probe runs in an
  ephemeral jail/container that is destroyed afterward.

## How we crawl

- Pagination over the registry API with a **persisted cursor** so a crash resumes
  rather than restarting.
- At most **4 concurrent** probes, with a polite delay between registry page fetches.
- User-Agent:
  `PlumblineCrawler/0.1 (+https://github.com/GautamTalksDev/Plumbline/blob/main/docs/CRAWLING.md)`
- Local/package execution (when used) runs only inside a sandbox: non-root,
  read-only rootfs, no host path mounts, default-deny network, memory ≤512 MiB,
  CPU capped, 60s wall clock then SIGKILL, ephemeral (no container reuse).
- Stdio/package probes use Docker/Podman when the daemon is available, otherwise
  `unshare` + `plumbline-jail` (pivot_root). Install the helper with
  `go install ./cmd/plumbline-jail` (or set `PLUMBLINE_JAIL_HELPER`).

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
