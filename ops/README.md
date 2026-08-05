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
```

### GitHub Actions cron (optional backup)

See [`.github/workflows/crawl-weekly.yml`](../.github/workflows/crawl-weekly.yml).
Requires secrets (`PLUMBLINE_SIGNING_KEY`, etc.) and a Linux runner that may
contact the public registry. Prefer systemd on a dedicated operator host;
Actions is a backup, not the source of truth for the six-week series.

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
