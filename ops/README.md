# Plumbline evidence ops (Phase E / CP-10)

**Do not launch. Do not post. Do not tweet. Six weeks.**

This directory is operational only. No product features land here.
Deferred product ideas go in [`../IDEAS.md`](../IDEAS.md) and stay closed.

## Goal

Accumulate a public Merkle log and a hand-labelled ground-truth set so
KT-1 and KT-2 can be scored with real numbers after six consecutive
**complete** weekly crawls with signed checkpoints. Incomplete runs
(`summary.json` → `complete: false`) do not count toward the series.

The weekly crawl is two-tier (packages population only): version poll,
then targeted spawn for version changes ∪ control cohort. See
[`docs/CRAWLING.md`](../docs/CRAWLING.md) and [`docs/COHORT.md`](../docs/COHORT.md).

## Weekly rhythm

| Day | Action |
|-----|--------|
| Crawl day (timer) | `scripts/run-weekly-crawl.sh` runs, writes `lab/runs/YYYY-MM-DD.*`, signs a checkpoint |
| Same day | `scripts/crawl-monitor.sh` appends monitor stats to the run folder |
| Same week, by hand | Export MEDIUM+ queue; label every row in `lab/labels/` |
| Same week, by hand | Append observations to `lab/NOTEBOOK.txt` |

## Schedule

### systemd (preferred on the operator Linux host)

```bash
# from repo root, as the operator user
mkdir -p ~/.config/systemd/user
cp ops/systemd/plumbline-crawl.service ops/systemd/plumbline-crawl.timer ~/.config/systemd/user/
# edit the service WorkingDirectory / EnvironmentFile paths if needed
systemctl --user daemon-reload
systemctl --user enable --now plumbline-crawl.timer
systemctl --user list-timers | grep plumbline
```

Environment file (not committed): `~/.plumbline/crawl.env`

```bash
PLUMBLINE_CRAWL_I_UNDERSTAND=1
PLUMBLINE_HOME=/home/YOU/.plumbline
PLUMBLINE_REPO=/path/to/Plumbline
# Signing key stays off the crawl box (default). After each complete run:
#   plumb sign-checkpoint --log ~/.plumbline/crawl-log.sqlite \
#     --signing-key ~/plumbline-offline/signing.ed25519 \
#     --out lab/runs/YYYY-MM-DD/checkpoint.json
# Or set PLUMBLINE_SIGNING_KEY_OFFLINE to that path and let the weekly script
# sign once at the end (still not co-resident during package execution if you
# only mount the key after the crawl exits — preferred).
PLUMBLINE_DEFER_SIGN=1
```

### Signing key separation

The machine that executes 479 strangers' packages must not hold the log
signing private key during the crawl. Default weekly script uses
`--defer-sign`. Keep `signing.ed25519` under `~/plumbline-offline/` (or a
hardware token / other host) and run `plumb sign-checkpoint` after each
complete run. Only the public key remains in-repo
(`docs/log-public-key.ed25519`) and as `~/.plumbline/signing.ed25519.pub`.

### Publish static site bundle

After signing the checkpoint, export the static JSON the site serves (no
live API required):

```bash
plumb publish-bundle --log ~/.plumbline/crawl-log.sqlite \
  --out site/log --week N \
  --pubkey docs/log-public-key.ed25519
plumb verify-log --bundle ./site/log --pubkey docs/log-public-key.ed25519
```

`--week` is the study week (1–6) shown in the site status banner via
`meta.json`. Observation files contain hashes and metadata only — never
tool descriptions or drift findings.

### Crawl host only (no GitHub Actions crawl)

Weekly crawl runs on the operator Linux host via systemd. There is **no**
GitHub Actions crawl workflow: a public (or even private) Actions job that
installs and executes arbitrary npm/PyPI packages on hosted runners is out
of scope. Do not re-add one.

## Monitoring (per run)

Recorded under `lab/runs/<date>/`:

- `summary.json` — atomic write: run id, complete flag, event counts, remotes excluded, duration, checkpoint
- `monitor.txt` — failure rate by type, duration, appear/disappear deltas vs prior run
- `checkpoint.json` — signed tree head copy (complete runs only)

## Hand labels (KT-2 evidence)

```bash
go run ./scripts/export-medium-plus.go ~/.plumbline/crawl-log.sqlite lab/labels/queue/
```

Then, by hand, fill `label` on every queued row: `correct`, `false_positive`,
or `missed`. Rubric: [`lab/labels/README.md`](../lab/labels/README.md).

## Kill tests

After six weekly runs: fill [`lab/kt-eval.txt`](../lab/kt-eval.txt) with
real counts (not estimates). Do not declare launch readiness before that.

## Correctness only

Fix crawler/log bugs that corrupt evidence. Every feature urge → `IDEAS.md`.
