#!/usr/bin/env bash
# The whole suite, as `go test -race -count=1 -timeout 20m ./...` runs it,
# in a third of the time.
#
# go test runs packages side by side, so the suite takes as long as its
# slowest package, and that is internal/server: ~400 tests, none of them
# parallel — most set environment variables with t.Setenv, which Go
# refuses in a parallel test — so they run one after another on one core,
# about ten minutes, while the rest of the suite is done in two or three.
#
# So that package is dealt into SHARDS groups by test name, each run as a
# go test process of its own, beside one process for everything else.
# Each test still runs exactly once, in a process of its own package, with
# the same flags: a separate process has its own environment, so nothing
# a test sets can reach a test in another shard. Every test go test -list
# finds is placed in exactly one shard, or nothing runs.
#
#   scripts/test.sh                 # 4 shards, -race -count=1 -timeout 20m
#   SHARDS=6 scripts/test.sh
#   scripts/test.sh -run TestFoo    # anything else goes to go test as it is, for every process
set -euo pipefail

cd "$(dirname "$0")/.."
SHARDS=${SHARDS:-4}
SLOW=./internal/server
FLAGS=(-race -count=1 -timeout 20m)
if [ $# -gt 0 ]; then FLAGS=("$@"); fi
if ! [[ "$SHARDS" =~ ^[1-9][0-9]*$ ]]; then
  echo "SHARDS=$SHARDS: a whole number above 0" >&2
  exit 2
fi

logs=$(mktemp -d "${TMPDIR:-/tmp}/conch-test.XXXXXX")
trap 'rm -rf "$logs"' EXIT

# The slow package's top-level tests (Test, Example and Fuzz functions all
# answer to -run), in a stable order.
go test -list '.*' "$SLOW" > "$logs/list" || { cat "$logs/list" >&2; exit 1; }
names=()
while IFS= read -r n; do names+=("$n"); done < <(grep -E '^(Test|Example|Fuzz)[A-Za-z0-9_]*$' "$logs/list" | sort -u)
listed=$(grep -cE '^(Test|Example|Fuzz)[A-Za-z0-9_]*$' "$logs/list" || true)
if [ "${#names[@]}" -eq 0 ] || [ "${#names[@]}" -ne "$listed" ]; then
  echo "go test -list $SLOW: ${#names[@]} distinct tests of $listed listed; refusing to shard" >&2
  exit 1
fi

# Dealt round-robin: tests next to each other by name tend to be alike in
# length, so each shard gets a share of the long ones.
groups=()
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

others=()
while IFS= read -r p; do others+=("$p"); done < <(go list ./... | grep -vx "github.com/Amitgb14/conch/internal/server")

start=$(date +%s)
pids=() labels=()
go test "${FLAGS[@]}" "${others[@]}" > "$logs/rest" 2>&1 &
pids+=($!) labels+=("rest")
for ((s = 0; s < SHARDS; s++)); do
  [ -n "${groups[$s]}" ] || continue
  go test "${FLAGS[@]}" -run "^(${groups[$s]})\$" "$SLOW" > "$logs/server-$s" 2>&1 &
  pids+=($!) labels+=("server-$s")
done

failed=0
for i in "${!pids[@]}"; do
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
  fi
done
cat "$logs/rest" | grep -E '^(ok|FAIL|---)' || true
for ((s = 0; s < SHARDS; s++)); do
  [ -f "$logs/server-$s" ] && grep -E '^(ok|FAIL)' "$logs/server-$s" | sed "s|internal/server|internal/server (shard $((s + 1))/$SHARDS)|" || true
done
echo "${#names[@]} internal/server tests in $SHARDS shards; $(( $(date +%s) - start ))s"
exit $failed
