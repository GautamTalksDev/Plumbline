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
- Observation outcome (success or a typed failure: timeout, connection refused,
  malformed payload, too large, protocol error, sandbox error, opt-out)
- Protocol version advertised during initialize
- A signed Merkle-log checkpoint at the end of each crawl run

Canonical hashes of tool metadata are appended to an append-only Merkle log.

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
