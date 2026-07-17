#!/usr/bin/env sh
set -eu

if [ "$#" -ne 1 ]; then
  echo "usage: $0 change-set.json" >&2
  exit 2
fi

critical='^(NLB|PostgreSQL|RuntimeSecret)$'
unsafe="$(jq -r --arg critical "$critical" '
  [(.Changes // .ResourceChanges // [])[]
   | (.ResourceChange // .) as $c
   | select(($c.LogicalResourceId // "") | test($critical))
   | select(($c.Action // "") == "Remove" or ($c.Replacement // "False") != "False")
   | "\($c.LogicalResourceId): action=\($c.Action) replacement=\($c.Replacement // "unknown")"]
  | .[]' "$1")"

if [ -n "$unsafe" ]; then
  echo "refusing a change set that removes or replaces protected resources:" >&2
  echo "$unsafe" >&2
  exit 1
fi
