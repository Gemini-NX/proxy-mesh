#!/usr/bin/env sh
set -eu

if [ "$#" -ne 2 ]; then
  echo "usage: DEVICE_ID=device-001 CONTROL_URL=http://control:8080 ADMIN_TOKEN=... $0 EXPECTED_VERSION ROUTE_JSON" >&2
  echo 'ROUTE_JSON example: {"host":"socks.example.net","port":1080,"username":"u","password":"p"}' >&2
  exit 2
fi

: "${DEVICE_ID:?DEVICE_ID is required}"
: "${CONTROL_URL:?CONTROL_URL is required}"
: "${ADMIN_TOKEN:?ADMIN_TOKEN is required}"

expected_version="$1"
route_json="$2"
request_file="$(mktemp)"
response_file="$(mktemp)"
trap 'rm -f "$request_file" "$response_file"' EXIT
chmod 600 "$request_file" "$response_file"

jq -e --argjson expectedVersion "$expected_version" '
  . + {expectedVersion:$expectedVersion} |
  if (.host | type) != "string" or (.host | length) == 0 then error("host is required")
  elif (.port | type) != "number" then error("port is required")
  elif (.password | type) != "string" then error("password is required")
  else .
  end
' "$route_json" > "$request_file"

status="$(curl -sS -o "$response_file" -w '%{http_code}' \
  -X PUT "$CONTROL_URL/v1/devices/$DEVICE_ID/route" \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H 'Content-Type: application/json' \
  --data-binary "@$request_file")"

case "$status" in
  200)
    jq . "$response_file"
    ;;
  *)
    jq 'if .error then {error} else {error:"route update failed"} end' "$response_file" >&2
    exit 1
    ;;
esac
