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
# Private signing key stays OFF the crawl box (see ops/README.md).
# Default: defer signing; operator signs after the run from an offline key path.
OPT_OUT="$ROOT/docs/opt-out.txt"
DEFER_SIGN="${PLUMBLINE_DEFER_SIGN:-1}"

echo "crawl start $DATE_UTC" | tee "$RUN_DIR/crawl.log"
START_EPOCH="$(date +%s)"

CRAWL_ARGS=(
  --state "$STATE"
  --log "$LOG"
  --opt-out "$OPT_OUT"
  --cohort "$ROOT/docs/cohort-v1-members.txt"
  --summary "$RUN_DIR/summary.json"
)
if [[ "$DEFER_SIGN" == "1" ]]; then
  CRAWL_ARGS+=(--defer-sign)
  echo "signing: deferred (key not on crawl box)" | tee -a "$RUN_DIR/crawl.log"
else
  KEY="${PLUMBLINE_SIGNING_KEY:-$HOME_PLUMB/signing.ed25519}"
  CRAWL_ARGS+=(--signing-key "$KEY")
fi

set +e
"$PLUMB_BIN" --json crawl \
  "${CRAWL_ARGS[@]}" \
  "$@" \
  >"$RUN_DIR/crawl-stdout.json" 2>>"$RUN_DIR/crawl.log"
EC=$?
set -e
END_EPOCH="$(date +%s)"
echo "crawl exit=$EC duration_s=$((END_EPOCH - START_EPOCH))" | tee -a "$RUN_DIR/crawl.log"

# Offline sign is manual: private key must not ride along with package execution.
if [[ "$DEFER_SIGN" == "1" ]]; then
  echo "NOTE: deferred sign — after verifying the run, from an offline key store run:" | tee -a "$RUN_DIR/crawl.log"
  echo "  plumb sign-checkpoint --log $LOG --signing-key <offline-key> --out $RUN_DIR/checkpoint.json" | tee -a "$RUN_DIR/crawl.log"
fi

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
