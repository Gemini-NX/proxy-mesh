#!/bin/sh
set -eu

compose_file="tests/integration/ss2022/docker-compose.yml"
cleanup() {
  docker compose -f "$compose_file" down -v >/dev/null 2>&1 || true
}
trap cleanup EXIT INT TERM

docker compose -f "$compose_file" up -d --build postgres control-plane upstream

until curl -fsS http://127.0.0.1:18081/live >/dev/null; do
  sleep 1
done

curl -fsS -X POST http://127.0.0.1:18081/v1/devices \
  -H 'Authorization: Bearer e2e-admin-token' \
  -H 'Content-Type: application/json' \
  -d '{"id":"device-ss2022","listenPort":51000,"shadowsocksMethod":"aes-256-gcm","shadowsocksPassword":"legacy-test-password"}' >/dev/null

docker compose -f "$compose_file" up -d --build gateway

until curl -fsS http://127.0.0.1:18180/ready >/dev/null; do
  sleep 1
done

until curl -fsS http://127.0.0.1:18081/v1/gateways \
  -H 'Authorization: Bearer e2e-admin-token' | grep -q 'READY'; do
  sleep 1
done

curl -fsS -X PUT http://127.0.0.1:18081/v1/devices/device-ss2022/route \
  -H 'Authorization: Bearer e2e-admin-token' \
  -H 'Content-Type: application/json' \
  -d '{"host":"upstream","port":1080,"username":"up-user","password":"up-pass","expectedVersion":0}' >/dev/null

curl -fsS -X POST http://127.0.0.1:18081/v1/devices/device-ss2022/ingresses \
  -H 'Authorization: Bearer e2e-admin-token' \
  -H 'Content-Type: application/json' \
  -d '{"listenPort":51001,"method":"2022-blake3-aes-128-gcm","password":"MDEyMzQ1Njc4OWFiY2RlZg=="}' >/dev/null

curl -fsS http://127.0.0.1:18081/v1/devices/device-ss2022/status \
  -H 'Authorization: Bearer e2e-admin-token' | grep -q '51001'

docker compose -f "$compose_file" up -d --build sing-box

until curl -fsS http://127.0.0.1:18180/ready >/dev/null; do
  sleep 1
done

until curl -fsS --socks5-hostname 127.0.0.1:19100 http://control-plane:8080/live | grep -q '"status":"ok"'; do
  sleep 1
done

curl -fsS -X DELETE \
  http://127.0.0.1:18081/v1/devices/device-ss2022/ingresses/51000 \
  -H 'Authorization: Bearer e2e-admin-token' >/dev/null

status="$(curl -fsS http://127.0.0.1:18081/v1/devices/device-ss2022/status \
  -H 'Authorization: Bearer e2e-admin-token')"
echo "$status" | grep -q '51001'
if echo "$status" | grep -q '51000'; then
  echo "legacy ingress was not retired" >&2
  exit 1
fi

curl -fsS --socks5-hostname 127.0.0.1:19100 \
  http://control-plane:8080/live | grep -q '"status":"ok"'

echo "Legacy -> parallel SS2022 -> retire legacy end-to-end test passed"
