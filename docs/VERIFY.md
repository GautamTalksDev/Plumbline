# Verifying a Plumbline transparency log

Anyone can check that a Plumbline Merkle log has not been tampered with using
only the CLI, a published Ed25519 public key, and HTTP access to a conforming
log endpoint. You do **not** need to trust the log operator.

## Quick start

```bash
# Build the CLI
go install github.com/GautamTalksDev/Plumbline/cmd/plumb@latest

# Verify a static publish-bundle (preferred for the public site)
plumb verify-log --bundle https://example.com/log/ \
  --pubkey-hex "$(curl -fsS https://example.com/log/pubkey.txt)"

# Or verify against any conforming live HTTP API
plumb verify-log --url https://log.example.com \
  --pubkey-hex "$(curl -fsS https://log.example.com/pubkey | jq -r .public_key)"
```

Exit code `0` means signatures and Merkle proofs checked out.
Exit code `1` means tampering or inconsistency was detected.

Official public key (also published on the site and at `GET /pubkey`):

```
32c9ceed20c77a7f6d995ec57d70fb6e8ae7293ce2aedb6cb5f1126548fdf50f
```

File copy in-repo: [`docs/log-public-key.ed25519`](log-public-key.ed25519).

## What `plumb verify-log` checks

1. Fetches `GET /checkpoint` and verifies the Ed25519 signature over the
   `plumbline-checkpoint-v1` payload with the public key.
2. Fetches historical `GET /checkpoints` and, for each consecutive pair,
   verifies signatures and `GET /proof/consistency?old=&new=`.
3. Downloads a prefix of `GET /entries?from=&to=`, recomputes each leaf hash
   from the canonical bytes, and verifies `GET /proof/inclusion?idx=` against
   the signed tree head.

If an operator rewrites history, alters a leaf, or serves a forged checkpoint,
verification fails.

## Static bundle inclusion-proof sampling (`plumbline-proofsample-v1`)

The public site ships a **static** bundle under `/log/`. Publishing one
inclusion proof per log entry exceeds static-hosting file caps, so
`plumb publish-bundle` emits a deterministic sample instead. Trust still
comes from the signed tree head and consistency proofs between
checkpoints; inclusion proofs are spot-checks (as in Certificate
Transparency).

With `--max-proofs N` (default **2000**), index `i` (`0 ≤ i < tree_size`)
is included in `proofs/inclusion/` iff any of:

1. `i < N`, or
2. `i` is among the last `min(500, tree_size)` indices, or
3. `uint64(SHA-256("plumbline-proofsample-v1|" + decimal(i))[0:8]) % tree_size < N`
   (first 8 bytes interpreted big-endian)

When `tree_size ≤ N`, every index is published. The published file
`proofs/inclusion/index.json` lists the sampled `indices`, their `count`,
`total_entries` (the full tree size), and `max_proofs`, so anyone can
recompute the set and confirm it was not hand-picked.

`plumb verify-log --bundle` samples only from that index (HTTP) or from
the proof files present (local). It never invents `0..tree_size`.

## HTTP API (any conforming endpoint)

| Method | Path | Purpose |
|--------|------|---------|
| GET | `/checkpoint` | Latest signed tree head |
| GET | `/checkpoints` | Historical signed checkpoints |
| GET | `/pubkey` | Ed25519 public key (hex) |
| GET | `/entries?from=&to=` | Paginated raw entries |
| GET | `/proof/inclusion?idx=` | Inclusion proof |
| GET | `/proof/consistency?old=&new=` | Consistency proof |
| GET | `/server/history?id=` | Observations for one server |
| GET | `/changes?since=&severity=` | Detected changes + rule IDs |

All endpoints are **read-only**. No authentication. No writes.

## Deploying the site

The static site lives in [`site/`](../site/). Put **Cloudflare** (free tier) in
front of `plumbd` for TLS, CDN caching, and edge rate limits.

GitHub Pages workflow: [`.github/workflows/pages.yml`](../.github/workflows/pages.yml).
After the first successful deploy the public site is at:

`https://gautamtalksdev.github.io/Plumbline/`

Set repository variable `PLUMBLINE_API_ORIGIN` only if you still expose a
live `plumbd` API; the Pages site serves the static `/log/` bundle by
default and needs no origin override for verification.

```bash
# After a crawl (or test log) exists:
plumbd -log ~/.plumbline/crawl-log.sqlite \
  -pubkey ~/.plumbline/signing.ed25519.pub \
  -site ./site \
  -addr :8080

plumb verify-log --url http://127.0.0.1:8080 \
  --pubkey ~/.plumbline/signing.ed25519.pub
```

Put Cloudflare (free tier) in front for TLS, caching, and edge rate limits.
`plumbd` also applies a per-IP request budget.

## Detecting tampering yourself

1. Serve a known-good log; confirm `plumb verify-log` exits 0.
2. Corrupt the sqlite (for example, alter a `leaf_hash` after disabling
   append-only triggers, or replace a checkpoint signature).
3. Restart `plumbd` and re-run `plumb verify-log` — it must exit 1.

Automated coverage: `go test ./internal/verify/ -run Tamper`.
