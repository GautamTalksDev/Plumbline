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

Foundations only. No application logic yet.

## Layout

```
cmd/plumb/           CLI entrypoint (cobra)
internal/canonical/  canonicalization + hashing (pure, no I/O)
internal/mcpclient/  MCP protocol client
internal/log/        merkle append-only log
internal/classify/   deterministic diff classifier
internal/crawler/    registry crawler
testdata/            fixtures
```

## License

Apache License 2.0. See [LICENSE](LICENSE) and [NOTICE](NOTICE).

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). DCO sign-off is required.

## Security

See [SECURITY.md](SECURITY.md).
