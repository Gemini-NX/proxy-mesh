#!/usr/bin/env sh
set -eu

if [ "$#" -ne 1 ]; then
  echo "usage: DEVICE_ID=device-001 ADMIN_TOKEN=... $0 /path/to/sing-box.json" >&2
  exit 2
fi
: "${DEVICE_ID:?DEVICE_ID is required}"
: "${ADMIN_TOKEN:?ADMIN_TOKEN is required}"

control_url="${CONTROL_URL:-http://127.0.0.1:8080}"
config_path="$1"
request_file="$(mktemp)"
response_file="$(mktemp)"
trap 'rm -f "$request_file" "$response_file"' EXIT
chmod 600 "$request_file" "$response_file"

jq -e --arg id "$DEVICE_ID" '
  first(.outbounds[] | select(.type == "shadowsocks")) as $ss |
  {
    id: $id,
    listenPort: $ss.server_port,
    shadowsocksMethod: $ss.method,
    shadowsocksPassword: $ss.password
  }
' "$config_path" > "$request_file"

status="$(curl -sS -o "$response_file" -w '%{http_code}' \
  -X POST "$control_url/v1/devices" \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H 'Content-Type: application/json' \
  --data-binary "@$request_file")"

if [ "$status" != "201" ]; then
  jq 'if .error then {error} else {error:"device import failed"} end' "$response_file" >&2
  exit 1
fi

jq 'del(.password) | if .singBoxOutbound then .singBoxOutbound.password = "<redacted>" else . end' "$response_file"
