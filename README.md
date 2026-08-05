# Plumbline

Plumbline was an attempt to observe post-publication changes in MCP server
tool metadata, hash them into an append-only Merkle log, and classify diffs
with a deterministic taxonomy.

## Kill tests (pre-registered, unchanged)

  KT-1: If fewer than 1% of crawled servers show any post-publication
        tool-metadata change over 6 weeks, this project is wrong and stops.
  KT-2: If the classifier cannot label >=80% of observed changes correctly
        as cosmetic while catching at least one capability-relevant change,
        it adds nothing over sha256sum and this project stops.

## Outcome

**Negative result.** See [RESULTS.md](RESULTS.md).

- **KT-1: FAIL** — six-week signed crawl series not completed; ≥1% drift over
  six weeks not established.
- **KT-2: FAIL** — no hand-labelled ground truth; ≥80% cosmetic-correctness
  bar not met.

Thresholds were not adjusted. `taxonomy-v1` was not refitted to the data.

This repository is retained as a transparent negative result and engineering
artifact (canonicalization, Merkle log, sandboxed crawler, verify-log). It is
not a launched product. Do not treat interim single-crawl stats as a pass of
the pre-registered study.

## License

Apache License 2.0. See [LICENSE](LICENSE) and [NOTICE](NOTICE).

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). DCO sign-off is required. New work that
reopens the kill tests must use a new pre-registration (e.g. taxonomy-v2), not
a silent refit of v1.
