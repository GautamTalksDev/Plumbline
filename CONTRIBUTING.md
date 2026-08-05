# Contributing to Plumbline

Thanks for your interest in contributing.

## Developer Certificate of Origin (DCO)

All contributions must be signed off under the [Developer Certificate of
Origin](https://developercertificate.org/).

Every commit message must include a `Signed-off-by` line:

```
Signed-off-by: Your Name <your.email@example.com>
```

Create signed-off commits with:

```bash
git commit -s
```

Unsigned commits will not be accepted. If you forgot `-s`, amend or recreate
the commit with a sign-off before opening a pull request.

By signing off, you certify that you have the right to submit the work under
the project's Apache License 2.0 and that the contribution is covered by the
DCO.

## How to contribute

1. Fork the repository and create a topic branch.
2. Make focused changes; keep PRs small and reviewable.
3. Ensure `go test ./...` and `golangci-lint run` pass locally.
4. Open a pull request with a clear description of *why* the change exists.
5. Ensure every commit in the PR is DCO-signed.

## Code of Conduct

Participation is governed by [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md).

## License

Contributions are licensed under the Apache License 2.0. See [LICENSE](LICENSE).
