#!/usr/bin/env sh
set -eu

control_url="${CONTROL_URL:-http://127.0.0.1:8080}"
gateway_url="${GATEWAY_HEALTH_URL:-http://127.0.0.1:18080}"
admin_token="${ADMIN_TOKEN:-local-admin-token}"

docker compose up -d --build postgres control-plane gateway

i=0
until curl -fsS "$control_url/live" >/dev/null && curl -fsS "$gateway_url/ready" >/dev/null; do
  i=$((i + 1))
  if [ "$i" -ge 60 ]; then
    docker compose ps >&2
    docker compose logs --no-color --tail=100 control-plane gateway >&2
    echo "ProxyMesh did not become ready within 120 seconds" >&2
    exit 1
  fi
  sleep 2
done

gateways="$(curl -fsS "$control_url/v1/gateways" -H "Authorization: Bearer $admin_token")"
if command -v jq >/dev/null 2>&1; then
	ready="$(printf '%s' "$gateways" | jq '[.[] | select(.status == "READY")] | length')"
	i=0
	while [ "$ready" -lt 1 ] && [ "$i" -lt 15 ]; do
		i=$((i + 1))
		sleep 1
		gateways="$(curl -fsS "$control_url/v1/gateways" -H "Authorization: Bearer $admin_token")"
		ready="$(printf '%s' "$gateways" | jq '[.[] | select(.status == "READY")] | length')"
	done
	[ "$ready" -ge 1 ] || {
    echo "Control Plane is live, but no Gateway is READY: $gateways" >&2
    exit 1
  }
fi

echo "ProxyMesh is ready"
echo "  Control API: $control_url"
echo "  Shadowsocks: 127.0.0.1:50000-50001"
echo "  Gateway:     $gateway_url/ready"
echo "  Gateways:    $gateways"
