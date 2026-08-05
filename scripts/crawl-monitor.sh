#!/usr/bin/env bash
# Post-crawl monitor: failure rates, duration, appear/disappear vs prior run.
set -euo pipefail

RUN_DIR="${1:?run dir}"
LOG="${2:?sqlite log}"
STATE="${3:?state json}"

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
OUT="$RUN_DIR/monitor.txt"
SUMMARY="$RUN_DIR/summary.json"

{
  echo "Plumbline crawl monitor"
  echo "generated_utc: $(date -u +%FT%TZ)"
  echo "run_dir: $RUN_DIR"
  echo

  if [[ -f "$SUMMARY" ]]; then
    echo "== summary.json =="
    python3 - <<'PY' "$SUMMARY"
import json,sys
p=sys.argv[1]
d=json.load(open(p))
counts=d.get("event_counts") or {}
done=int(d.get("servers_done") or 0)
seen=int(d.get("servers_seen") or 0)
dur=d.get("duration") or d.get("Duration") or ""
# duration may be Go string like "1h2m3s" or nanoseconds number
print(f"servers_seen: {seen}")
print(f"servers_done: {done}")
print(f"tools_logged: {d.get('tools_logged')}")
print(f"resumed: {d.get('resumed')}")
print(f"duration: {dur}")
total=sum(int(v) for v in counts.values()) or 1
print("event_counts:")
for k in sorted(counts):
    n=int(counts[k])
    print(f"  {k}: {n} ({100.0*n/total:.1f}%)")
fail=sum(int(counts.get(k,0)) for k in counts if k not in ("ok","opt_out"))
print(f"failure_rate_excl_opt_out: {100.0*fail/total:.1f}% ({fail}/{total})")
cp=d.get("checkpoint") or {}
if cp:
    print(f"checkpoint_tree_size: {cp.get('TreeSize') or cp.get('tree_size')}")
    print(f"checkpoint_root: {cp.get('RootHash') or cp.get('root_hash')}")
PY
    echo
  else
    echo "summary.json missing"
    echo
  fi

  echo "== state =="
  python3 - <<'PY' "$STATE"
import json,sys,os
p=sys.argv[1]
if not os.path.exists(p):
    print("state missing"); raise SystemExit
d=json.load(open(p))
completed=d.get("completed") or {}
inflight=d.get("in_flight") or {}
queue=d.get("queue") or []
print(f"page_cursor_set: {bool(d.get('page_cursor'))}")
print(f"completed_servers: {len(completed)}")
print(f"in_flight: {len(inflight)}")
print(f"queue_len: {len(queue)}")
PY
  echo

  echo "== appear / disappear vs prior run =="
  python3 - <<'PY' "$ROOT" "$RUN_DIR" "$STATE"
import json,sys,os,glob
root, run_dir, state_path = sys.argv[1], sys.argv[2], sys.argv[3]
runs=sorted(glob.glob(os.path.join(root,"lab","runs","*")))
# prior = previous date dir
dates=[os.path.basename(r) for r in runs if os.path.isdir(r)]
cur=os.path.basename(run_dir.rstrip("/"))
priors=[d for d in dates if d < cur]
if not os.path.exists(state_path):
    print("no state"); raise SystemExit
cur_set=set((json.load(open(state_path)).get("completed") or {}).keys())
if not priors:
    print(f"first run with state; completed={len(cur_set)}")
    raise SystemExit
prev_state=None
# Prefer prior run's completed snapshot if saved
prev_snap=os.path.join(root,"lab","runs",priors[-1],"completed_servers.txt")
if os.path.exists(prev_snap):
    prev=set(x.strip() for x in open(prev_snap) if x.strip())
else:
    print(f"no completed_servers.txt in {priors[-1]}; cannot diff yet")
    raise SystemExit
appeared=sorted(cur_set - prev)
disappeared=sorted(prev - cur_set)
print(f"prior_run: {priors[-1]}")
print(f"appeared: {len(appeared)}")
for s in appeared[:50]:
    print(f"  + {s}")
if len(appeared)>50: print(f"  … {len(appeared)-50} more")
print(f"disappeared: {len(disappeared)}")
for s in disappeared[:50]:
    print(f"  - {s}")
if len(disappeared)>50: print(f"  … {len(disappeared)-50} more")
PY

  # Snapshot completed set for next week's diff.
  python3 - <<'PY' "$STATE" "$RUN_DIR/completed_servers.txt"
import json,sys
d=json.load(open(sys.argv[1]))
names=sorted((d.get("completed") or {}).keys())
open(sys.argv[2],"w").write("\n".join(names)+("\n" if names else ""))
print(f"wrote {len(names)} servers to completed_servers.txt")
PY

} | tee "$OUT"

# Copy checkpoint from summary if present
python3 - <<'PY' "$SUMMARY" "$RUN_DIR/checkpoint.json"
import json,sys,os
src,dst=sys.argv[1],sys.argv[2]
if not os.path.exists(src):
    raise SystemExit
d=json.load(open(src))
cp=d.get("checkpoint")
if cp:
    json.dump(cp, open(dst,"w"), indent=2)
    open(dst,"a").write("\n")
PY

echo "wrote $OUT"
