#!/bin/sh
set -eu

log=$(mktemp)
trap 'rm -f "$log"' EXIT HUP INT TERM
if sudo apt-get update >"$log" 2>&1 &&
  sudo apt-get install -y --no-install-recommends "$@" >>"$log" 2>&1; then
  cat "$log"
else
  cat "$log"
  # Infrastructure failures must be distinguishable from test regressions even
  # when an API client cannot follow GitHub's signed job-log download redirect.
  grep -E '^(E:|Err:)' "$log" | tail -n 12 | while IFS= read -r line; do
    escaped=$(printf '%s' "$line" | sed 's/%/%25/g')
    printf '::error title=Test dependency installation::%s\n' "$escaped"
  done
  exit 1
fi
