#!/usr/bin/env bash
# Operator weekly crawl + evidence snapshot. No product features.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

HOME_PLUMB="${PLUMBLINE_HOME:-$HOME/.plumbline}"
mkdir -p "$HOME_PLUMB" "$ROOT/lab/runs"

DATE_UTC="$(date -u +%F)"
RUN_DIR="$ROOT/lab/runs/$DATE_UTC"
mkdir -p "$RUN_DIR"

if [[ "${PLUMBLINE_CRAWL_I_UNDERSTAND:-}" != "1" ]]; then
  if [[ -f "$HOME_PLUMB/crawl.env" ]]; then
    # shellcheck disable=SC1090
    set -a
    source "$HOME_PLUMB/crawl.env"
    set +a
  fi
fi
if [[ "${PLUMBLINE_CRAWL_I_UNDERSTAND:-}" != "1" ]]; then
  echo "refuse: set PLUMBLINE_CRAWL_I_UNDERSTAND=1 (see docs/CRAWLING.md)" >&2
  exit 2
fi

# Ensure jail helper is on PATH when unshare sandbox is used.
if ! command -v plumbline-jail >/dev/null 2>&1; then
  go build -o "$HOME_PLUMB/bin/plumbline-jail" ./cmd/plumbline-jail
  export PATH="$HOME_PLUMB/bin:$PATH"
  export PLUMBLINE_JAIL_HELPER="$HOME_PLUMB/bin/plumbline-jail"
fi

PLUMB_BIN="${PLUMB_BIN:-}"
if [[ -z "$PLUMB_BIN" ]]; then
  go build -o "$HOME_PLUMB/bin/plumb" ./cmd/plumb
  PLUMB_BIN="$HOME_PLUMB/bin/plumb"
fi

STATE="$HOME_PLUMB/crawl-state.json"
LOG="$HOME_PLUMB/crawl-log.sqlite"
KEY="$HOME_PLUMB/signing.ed25519"
OPT_OUT="$ROOT/docs/opt-out.txt"

echo "crawl start $DATE_UTC" | tee "$RUN_DIR/crawl.log"
START_EPOCH="$(date +%s)"

set +e
"$PLUMB_BIN" --json crawl \
  --state "$STATE" \
  --log "$LOG" \
  --signing-key "$KEY" \
  --opt-out "$OPT_OUT" \
  --cohort "$ROOT/docs/cohort-v1-members.txt" \
  --summary "$RUN_DIR/summary.json" \
  >"$RUN_DIR/crawl-stdout.json" 2>>"$RUN_DIR/crawl.log"
EC=$?
set -e
END_EPOCH="$(date +%s)"
echo "crawl exit=$EC duration_s=$((END_EPOCH - START_EPOCH))" | tee -a "$RUN_DIR/crawl.log"

# Monitor + notebook stub even on non-zero (partial runs still evidence).
"$ROOT/scripts/crawl-monitor.sh" "$RUN_DIR" "$LOG" "$STATE" || true

if [[ ! -f "$ROOT/lab/NOTEBOOK.txt" ]]; then
  printf 'Plumbline lab notebook\n=====================\n\n' >"$ROOT/lab/NOTEBOOK.txt"
fi
{
  echo "----"
  echo "date: $DATE_UTC"
  echo "run_dir: lab/runs/$DATE_UTC"
  echo "exit: $EC"
  echo "observations: (fill by hand this week)"
  echo "surprises: (fill by hand this week)"
  echo
} >>"$ROOT/lab/NOTEBOOK.txt"

# Export MEDIUM+ queue for hand labelling (KT-2).
go run ./scripts/export-medium-plus.go "$LOG" "$ROOT/lab/labels/queue" || true

exit "$EC"
