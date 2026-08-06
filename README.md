# Plumbline

Plumbline observes post-publication changes in MCP server tool metadata.
It canonicalizes descriptions, hashes them into an append-only merkle log,
and classifies diffs as cosmetic or capability-relevant.

## Kill Tests

  KT-1: If fewer than 1% of crawled servers show any post-publication
        tool-metadata change over 6 weeks, this project is wrong and stops.
  KT-2: If the classifier cannot label >=80% of observed changes correctly
        as cosmetic while catching at least one capability-relevant change,
        it adds nothing over sha256sum and this project stops.

## Status

Phase E (evidence). Weekly crawl ops live under `ops/` and `lab/`.
**Do not launch. Do not post.** Kill-test evaluation waits on six consecutive
weekly crawls with signed checkpoints and a hand-labelled MEDIUM+ set —
see [RESULTS.md](RESULTS.md).

- Crawl policy: [docs/CRAWLING.md](docs/CRAWLING.md)
- Ethics (Menlo Report alignment): [ETHICS.md](ETHICS.md)
- Study limitations at freeze: [docs/LIMITATIONS.md](docs/LIMITATIONS.md)
- Verify any log endpoint: [docs/VERIFY.md](docs/VERIFY.md)
- Log public key: [docs/log-public-key.ed25519](docs/log-public-key.ed25519)
- Deferred ideas: [IDEAS.md](IDEAS.md) (do not build during Phase E)

## Layout

```
cmd/plumb/             CLI (check, accept, crawl, verify-log)
cmd/plumbd/            read-only transparency log HTTP server
cmd/plumbline-jail/    Linux pivot_root helper for unshare sandbox
internal/canonical/    canonicalization + hashing
internal/mcpclient/    MCP protocol client
internal/log/          merkle append-only log
internal/classify/     deterministic diff classifier
internal/crawler/      sandboxed registry crawler
internal/httpserver/   public log HTTP API
internal/verify/       remote log verifier (any endpoint)
site/                  static transparency site
ops/                   weekly crawl schedule + operator docs
lab/                   notebook, run snapshots, hand labels (KT-2)
IDEAS.md               deferred features (do not build in Phase E)
docs/                  CRAWLING, VERIFY, public key
testdata/              fixtures
```

## License

Apache License 2.0. See [LICENSE](LICENSE) and [NOTICE](NOTICE).

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). DCO sign-off is required.

## Security

See [SECURITY.md](SECURITY.md).
