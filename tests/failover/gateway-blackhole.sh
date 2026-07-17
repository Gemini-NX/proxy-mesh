#!/usr/bin/env sh
set -eu

: "${GATEWAY_HEALTH_URL:?internal health URL is required}"
: "${PROXY_URL:?public proxy URL is required}"
: "${DEVICE_USERNAME:?test device username is required}"
: "${DEVICE_PASSWORD:?test device password is required}"
: "${CONNECT_TARGET:?TCP target host:port is required}"

curl -fsS -X POST "$GATEWAY_HEALTH_URL/drain"
if curl -fsS "$GATEWAY_HEALTH_URL/ready" >/dev/null 2>&1; then
  echo "drained gateway remained ready" >&2
  exit 1
fi

# A fresh request through NLB must be served by another healthy Gateway.
curl -fsS --connect-timeout 10 --max-time 30 \
  --proxy "$PROXY_URL" \
  --proxy-user "$DEVICE_USERNAME:$DEVICE_PASSWORD" \
  "https://$CONNECT_TARGET/" >/dev/null

echo "failover request succeeded while selected gateway was draining"
