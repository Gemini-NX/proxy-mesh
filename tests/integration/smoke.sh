#!/usr/bin/env sh
set -eu

: "${CONTROL_URL:?CONTROL_URL is required}"
: "${ADMIN_TOKEN:?ADMIN_TOKEN is required}"

curl -fsS "$CONTROL_URL/live" >/dev/null
gateways="$(curl -fsS "$CONTROL_URL/v1/gateways" -H "Authorization: Bearer $ADMIN_TOKEN")"
ready="$(printf '%s' "$gateways" | jq '[.[] | select(.status == "READY")] | length')"
total="$(printf '%s' "$gateways" | jq 'length')"
[ "$total" -ge 3 ] || { echo "fewer than three gateways registered" >&2; exit 1; }
[ "$ready" -ge 3 ] || { echo "fewer than three gateways ready" >&2; exit 1; }
echo "smoke passed: $ready/$total gateways ready"
