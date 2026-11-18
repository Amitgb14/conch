#!/usr/bin/env bash
# The whole suite, as `go test -race -count=1 -timeout 20m ./...` runs it,
# in a third of the time.
#
# go test runs packages side by side, so the suite takes as long as its
# slowest package, and that is internal/server: ~580 tests, none of them
# parallel — most set environment variables with t.Setenv, which Go
# refuses in a parallel test — so they run one after another on one core,
# ~15 minutes on a CI runner, while the rest of the suite is done in four.
#
# So that package is dealt into SHARDS groups by test name, each run as a
# go test process of its own, beside one process for everything else.
# Each test still runs exactly once, in a process of its own package, with
# the same flags: a separate process has its own environment, so nothing
# a test sets can reach a test in another shard. Every test go test -list
# finds is placed in exactly one shard, or nothing runs.
#
# PART runs some of that alone, which is how CI spreads it over
# machines: a comma-separated list of "rest" (every package but
# internal/server) and "server-K" (shard K of SHARDS). Run every part, with
# the same SHARDS, and you have run the whole suite once —
# .github/workflows/ci.yml lists them for each system, and
# TestCIRunsEveryPart says it lists all of them.
#
#   scripts/test.sh                 # 4 shards, -race -count=1 -timeout 20m
#   SHARDS=6 scripts/test.sh
#   PART=rest scripts/test.sh       # everything but internal/server
#   SHARDS=5 PART=server-2 scripts/test.sh            # the second of five shards
#   SHARDS=4 PART=server-1,server-2 scripts/test.sh   # two of four, side by side
#   scripts/test.sh -run TestFoo    # anything else goes to go test as it is, for every process
set -euo pipefail

cd "$(dirname "$0")/.."
SHARDS=${SHARDS:-4}
PART=${PART:-all}
SLOW=./internal/server
FLAGS=(-race -count=1 -timeout 20m)
if [ $# -gt 0 ]; then FLAGS=("$@"); fi
if ! [[ "$SHARDS" =~ ^[1-9][0-9]*$ ]]; then
  echo "SHARDS=$SHARDS: a whole number above 0" >&2
  exit 2
fi

# Which processes this run starts: the rest, and which shards of the slow
# package (0-based).
run_rest=0
run_shards=()
if [ "$PART" = all ]; then
  run_rest=1
  for ((s = 0; s < SHARDS; s++)); do run_shards+=("$s"); done
else
  # A list, so one machine can run several shards side by side: CI's macOS
  # runners are few, and each has cores to spare.
  IFS=, read -r -a items <<<"$PART"
  if [ ${#items[@]} -eq 0 ]; then
    echo "PART=$PART: all, or a list of rest and server-K" >&2
    exit 2
  fi
  seen=","
  for item in "${items[@]}"; do
    case "$item" in
    rest) run_rest=1 ;;
    server-*)
      k=${item#server-}
      if ! [[ "$k" =~ ^[1-9][0-9]*$ ]] || [ "$k" -gt "$SHARDS" ]; then
        echo "PART=$PART: server-K with K from 1 to SHARDS=$SHARDS" >&2
        exit 2
      fi
      run_shards+=($((k - 1)))
      ;;
    *)
      echo "PART=$PART: all, or a list of rest and server-K" >&2
      exit 2
      ;;
    esac
    # Named twice would run its tests twice, side by side in one process
    # environment each — harmless, but a sign the list is not what was meant.
    if [[ "$seen" == *",$item,"* ]]; then
      echo "PART=$PART: $item named twice" >&2
      exit 2
    fi
    seen+="$item,"
  done
fi

logs=$(mktemp -d "${TMPDIR:-/tmp}/conch-test.XXXXXX")
trap 'rm -rf "$logs"' EXIT

groups=()
names=()
if [ ${#run_shards[@]} -gt 0 ]; then
  # The slow package's top-level tests (Test, Example and Fuzz functions all
  # answer to -run), in a stable order.
  go test -list '.*' "$SLOW" > "$logs/list" || { cat "$logs/list" >&2; exit 1; }
  while IFS= read -r n; do names+=("$n"); done < <(grep -E '^(Test|Example|Fuzz)[A-Za-z0-9_]*$' "$logs/list" | sort -u)
  listed=$(grep -cE '^(Test|Example|Fuzz)[A-Za-z0-9_]*$' "$logs/list" || true)
  if [ "${#names[@]}" -eq 0 ] || [ "${#names[@]}" -ne "$listed" ]; then
    echo "go test -list $SLOW: ${#names[@]} distinct tests of $listed listed; refusing to shard" >&2
    exit 1
  fi

  # Dealt round-robin: tests next to each other by name tend to be alike in
  # length, so each shard gets a share of the long ones.
  for ((s = 0; s < SHARDS; s++)); do groups+=(""); done
  for i in "${!names[@]}"; do
    s=$((i % SHARDS))
    groups[$s]+="${groups[$s]:+|}${names[$i]}"
  done
  placed=0
  for g in "${groups[@]}"; do
    [ -n "$g" ] && placed=$((placed + $(tr '|' '\n' <<<"$g" | wc -l)))
  done
  if [ "$placed" -ne "${#names[@]}" ]; then
    echo "placed $placed of ${#names[@]} tests; refusing to run" >&2
    exit 1
  fi
fi

start=$(date +%s)
pids=() labels=()
if [ "$run_rest" = 1 ]; then
  others=()
  while IFS= read -r p; do others+=("$p"); done < <(go list ./... | grep -vx "github.com/Amitgb14/conch/internal/server")
  go test "${FLAGS[@]}" "${others[@]}" > "$logs/rest" 2>&1 &
  pids+=($!) labels+=("rest")
fi
for s in ${run_shards[@]+"${run_shards[@]}"}; do
  # A shard dealt nothing (more shards than tests) has nothing to run;
  # -run with an empty pattern would run them all.
  [ -n "${groups[$s]}" ] || continue
  go test "${FLAGS[@]}" -run "^(${groups[$s]})\$" "$SLOW" > "$logs/server-$s" 2>&1 &
  pids+=($!) labels+=("server-$s")
done

failed=0
for i in ${pids[@]+"${!pids[@]}"}; do
  if ! wait "${pids[$i]}"; then
    failed=1
    # Kept: the temporary folder goes when this ends, and a failure's
    # messages are the one thing worth reading afterwards.
    kept="${TMPDIR:-/tmp}/conch-test-${labels[$i]}.log"
    cp "$logs/${labels[$i]}" "$kept"
    echo "=== ${labels[$i]} failed (whole log: $kept):" >&2
    # Each failing test with what it said, and a timeout's running tests.
    grep -E -A8 '^--- FAIL|^panic: ' "$logs/${labels[$i]}" >&2 || true
    grep -E '^FAIL' "$logs/${labels[$i]}" >&2 || true
    # Under CI the log file goes with the runner, so the whole of it is
    # printed: a failure must be readable from the job's own output.
    if [ -n "${CI:-}" ]; then cat "$logs/${labels[$i]}" >&2; fi
  fi
done
[ -f "$logs/rest" ] && { grep -E '^(ok|FAIL|---)' "$logs/rest" || true; }
for ((s = 0; s < SHARDS; s++)); do
  [ -f "$logs/server-$s" ] && { grep -E '^(ok|FAIL)' "$logs/server-$s" | sed "s|internal/server|internal/server (shard $((s + 1))/$SHARDS)|" || true; }
done
if [ ${#names[@]} -gt 0 ]; then
  echo "PART=$PART: ${#names[@]} internal/server tests in $SHARDS shards; $(($(date +%s) - start))s"
else
  echo "PART=$PART: $(($(date +%s) - start))s"
fi
exit $failed
