#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat >&2 <<'USAGE'
usage: scripts/aliyun-data-plane-canary.sh STACK_ID_OR_NAME

Runs a staging data-plane canary against Alibaba Cloud:
  1. starts a temporary authenticated SOCKS5 server on every Gateway ECS
  2. creates or rotates a disposable Shadowsocks device through the private
     Control Plane API from inside the Control ECS
  3. publishes a route to the Docker bridge host on port 19080, acknowledged by all Gateways
  4. starts a temporary local sing-box client and connects to the public NLB Shadowsocks port
     and verifies an HTTP request through Gateway -> SOCKS5 -> example.com:80
  5. rotates the disposable device password again and stops the temporary
     SOCKS5 servers

Environment:
  REGION=cn-hongkong
  ALIYUN_PROFILE=hz
  ALIYUN_BIN=aliyun
  DOCKER_BIN=docker
  SING_BOX_IMAGE=ghcr.io/sagernet/sing-box:latest
  CANARY_DEVICE_ID=staging-e2e
  CANARY_INGRESS_PORT=59998
  CANARY_UPSTREAM_PORT=19080
  CANARY_UPSTREAM_HOST=172.17.0.1
  CANARY_METHOD=aes-256-gcm
  CANARY_ROTATE_AFTER=true
USAGE
}

if [ "$#" -ne 1 ]; then
  usage
  exit 2
fi

stack_ref="$1"
region="${REGION:-cn-hongkong}"
profile="${ALIYUN_PROFILE:-default}"
aliyun_bin="${ALIYUN_BIN:-aliyun}"
docker_bin="${DOCKER_BIN:-docker}"
sing_box_image="${SING_BOX_IMAGE:-ghcr.io/sagernet/sing-box:latest}"
device_id="${CANARY_DEVICE_ID:-staging-e2e}"
ingress_port="${CANARY_INGRESS_PORT:-59998}"
upstream_port="${CANARY_UPSTREAM_PORT:-19080}"
upstream_host="${CANARY_UPSTREAM_HOST:-172.17.0.1}"
method="${CANARY_METHOD:-aes-256-gcm}"
rotate_after="${CANARY_ROTATE_AFTER:-true}"
upstream_user="proxymesh-canary"
upstream_pass="proxymesh-canary-pass"

resolve_stack_id() {
  case "$stack_ref" in
    *-*-*-*-*) printf '%s\n' "$stack_ref"; return 0 ;;
  esac
  "$aliyun_bin" ros ListStacks --profile "$profile" --RegionId "$region" \
    --StackName.1 "$stack_ref" \
    --Status.1 CREATE_COMPLETE \
    --Status.2 UPDATE_COMPLETE \
    --Status.3 CREATE_IN_PROGRESS \
    --Status.4 UPDATE_IN_PROGRESS |
    jq -er --arg name "$stack_ref" '
      [.Stacks[] | select(.StackName == $name)] |
      sort_by(.CreateTime) |
      last.StackId
    '
}

run_command() {
  local command_content="$1"
  shift
  local -a instance_args=()
  local index=1
  for instance_id in "$@"; do
    instance_args+=("--InstanceId.${index}" "$instance_id")
    index=$((index + 1))
  done
  local command_id
  command_id="$("$aliyun_bin" ecs RunCommand \
    --profile "$profile" \
    --RegionId "$region" \
    --Type RunShellScript \
    --CommandContent "$command_content" \
    "${instance_args[@]}" |
    jq -er '.CommandId')"
  local result status
  for _ in {1..90}; do
    result="$("$aliyun_bin" ecs DescribeInvocationResults \
      --profile "$profile" \
      --RegionId "$region" \
      --CommandId "$command_id")"
    status="$(jq -r '
      [.Invocation.InvocationResults.InvocationResult[].InvocationStatus] |
      if length == 0 then "Pending"
      elif all(. == "Success" or . == "Failed" or . == "Stopped") then "Done"
      else "Running"
      end
    ' <<<"$result")"
    [ "$status" = "Done" ] && break
    sleep 2
  done
  local failed
  failed="$(jq '[.Invocation.InvocationResults.InvocationResult[] | select((.ExitCode // 255) != 0)] | length' <<<"$result")"
  if [ "$failed" != "0" ]; then
    jq -r '
      .Invocation.InvocationResults.InvocationResult[] |
      select((.ExitCode // 255) != 0) |
      "instance=" + .InstanceId + " exit=" + ((.ExitCode // 255) | tostring) + "\n" + ((.Output // "" | @base64d) // "")
    ' <<<"$result" >&2
    return 1
  fi
  jq -r '.Invocation.InvocationResults.InvocationResult[] | .Output // "" | @base64d' <<<"$result"
}

stack_id="$(resolve_stack_id)"
stack="$("$aliyun_bin" ros GetStack --profile "$profile" --RegionId "$region" --StackId "$stack_id")"
control_instance_id="$(jq -er '.Outputs[] | select(.OutputKey == "ControlInstanceId") | .OutputValue' <<<"$stack")"
scaling_group_id="$(jq -er '.Outputs[] | select(.OutputKey == "GatewayScalingGroupId") | .OutputValue' <<<"$stack")"
nlb_dns="$(jq -er '.Outputs[] | select(.OutputKey == "NLBDNSName") | .OutputValue' <<<"$stack")"

mapfile -t gateway_instance_ids < <("$aliyun_bin" ess DescribeScalingInstances \
  --profile "$profile" \
  --RegionId "$region" \
  --ScalingGroupId "$scaling_group_id" |
  jq -er '.ScalingInstances.ScalingInstance[] | select(.LifecycleState == "InService" and .HealthStatus == "Healthy") | .InstanceId')

if [ "${#gateway_instance_ids[@]}" -lt 1 ]; then
  echo "no healthy Gateway instances found in $scaling_group_id" >&2
  exit 1
fi

start_socks_command="$(cat <<'REMOTE'
set -eu
port="__UPSTREAM_PORT__"
user="__UPSTREAM_USER__"
pass="__UPSTREAM_PASS__"
pkill -f /tmp/proxymesh-canary-socks.py 2>/dev/null || true
cat >/tmp/proxymesh-canary-socks.py <<'PY'
import select
import socket
import socketserver
import struct
import sys
import threading

HOST, PORT, USER, PASS = sys.argv[1], int(sys.argv[2]), sys.argv[3].encode(), sys.argv[4].encode()

def recv_exact(conn, n):
    data = b""
    while len(data) < n:
        chunk = conn.recv(n - len(data))
        if not chunk:
            raise OSError("unexpected eof")
        data += chunk
    return data

class Handler(socketserver.BaseRequestHandler):
    def handle(self):
        c = self.request
        c.settimeout(10)
        head = recv_exact(c, 2)
        if head[0] != 5:
            return
        methods = recv_exact(c, head[1])
        if 2 not in methods:
            c.sendall(b"\x05\xff")
            return
        c.sendall(b"\x05\x02")
        auth = recv_exact(c, 2)
        if auth[0] != 1:
            return
        username = recv_exact(c, auth[1])
        plen = recv_exact(c, 1)[0]
        password = recv_exact(c, plen)
        if username != USER or password != PASS:
            c.sendall(b"\x01\x01")
            return
        c.sendall(b"\x01\x00")
        req = recv_exact(c, 4)
        if req[0] != 5 or req[1] != 1:
            return
        atyp = req[3]
        if atyp == 1:
            host = socket.inet_ntoa(recv_exact(c, 4))
        elif atyp == 3:
            host = recv_exact(c, recv_exact(c, 1)[0]).decode()
        elif atyp == 4:
            host = socket.inet_ntop(socket.AF_INET6, recv_exact(c, 16))
        else:
            return
        port = struct.unpack("!H", recv_exact(c, 2))[0]
        upstream = socket.create_connection((host, port), timeout=10)
        c.sendall(b"\x05\x00\x00\x01\x00\x00\x00\x00\x00\x00")
        c.settimeout(None)
        upstream.settimeout(None)
        sockets = [c, upstream]
        while True:
            readable, _, _ = select.select(sockets, [], [], 60)
            if not readable:
                return
            for src in readable:
                dst = upstream if src is c else c
                data = src.recv(65536)
                if not data:
                    return
                dst.sendall(data)

class Server(socketserver.ThreadingTCPServer):
    allow_reuse_address = True

Server((HOST, PORT), Handler).serve_forever()
PY
nohup python3 /tmp/proxymesh-canary-socks.py 0.0.0.0 "$port" "$user" "$pass" >/tmp/proxymesh-canary-socks.log 2>&1 &
sleep 1
python3 - <<PY
import socket
s=socket.create_connection(("127.0.0.1", int("$port")), timeout=3)
s.close()
PY
echo "temporary SOCKS5 ready on 0.0.0.0:$port"
REMOTE
)"
start_socks_command="${start_socks_command//__UPSTREAM_PORT__/$upstream_port}"
start_socks_command="${start_socks_command//__UPSTREAM_USER__/$upstream_user}"
start_socks_command="${start_socks_command//__UPSTREAM_PASS__/$upstream_pass}"

stop_socks_command='pkill -f /tmp/proxymesh-canary-socks.py 2>/dev/null || true; rm -f /tmp/proxymesh-canary-socks.py /tmp/proxymesh-canary-socks.log; echo "temporary SOCKS5 stopped"'

cleanup() {
  run_command "$stop_socks_command" "${gateway_instance_ids[@]}" >/dev/null 2>&1 || true
}
trap cleanup EXIT

echo "starting temporary SOCKS5 upstream on ${#gateway_instance_ids[@]} Gateway instance(s)" >&2
run_command "$start_socks_command" "${gateway_instance_ids[@]}" >/dev/null

configure_command="$(cat <<'REMOTE'
set -eu
device_id="__DEVICE_ID__"
ingress_port="__INGRESS_PORT__"
method="__METHOD__"
upstream_port="__UPSTREAM_PORT__"
upstream_host="__UPSTREAM_HOST__"
upstream_user="__UPSTREAM_USER__"
upstream_pass="__UPSTREAM_PASS__"
token="$(sed -n 's/^ADMIN_TOKEN=//p' /run/proxymesh/control.env)"
test -n "$token"
control=http://127.0.0.1:8080
ss_pass="$(openssl rand -base64 24 | tr -d '\n')"
request="$(mktemp)"
response="$(mktemp)"
trap 'rm -f "$request" "$response"' EXIT
jq -nc \
  --arg id "$device_id" \
  --argjson port "$ingress_port" \
  --arg method "$method" \
  --arg password "$ss_pass" \
  '{id:$id,listenPort:$port,shadowsocksMethod:$method,shadowsocksPassword:$password}' >"$request"
status="$(curl -sS -o "$response" -w '%{http_code}' \
  -X POST "$control/v1/devices" \
  -H "Authorization: Bearer $token" \
  -H 'Content-Type: application/json' \
  --data-binary "@$request")"
case "$status" in
  201) ;;
  409|500)
    if [ "$status" = "500" ] && ! jq -e '.error == "storage operation failed"' "$response" >/dev/null; then
      cat "$response" >&2
      exit 1
    fi
    curl -fsS -o "$response" \
      -X POST "$control/v1/devices/$device_id/credentials/rotate" \
      -H "Authorization: Bearer $token"
    ss_pass="$(jq -er '.password' "$response")"
    ;;
  *)
    cat "$response" >&2
    exit 1
    ;;
esac
status_json="$(curl -fsS "$control/v1/devices/$device_id/status" -H "Authorization: Bearer $token")"
expected="$(printf '%s' "$status_json" | jq -r '.route.version // 0')"
jq -nc \
  --arg host "$upstream_host" \
  --argjson port "$upstream_port" \
  --arg username "$upstream_user" \
  --arg password "$upstream_pass" \
  --argjson expectedVersion "$expected" \
  '{host:$host,port:$port,username:$username,password:$password,expectedVersion:$expectedVersion}' >"$request"
curl -fsS -o "$response" \
  -X PUT "$control/v1/devices/$device_id/route" \
  -H "Authorization: Bearer $token" \
  -H 'Content-Type: application/json' \
  --data-binary "@$request"
version="$(jq -er '.version' "$response")"
jq -nc \
  --arg deviceId "$device_id" \
  --argjson listenPort "$ingress_port" \
  --arg method "$method" \
  --arg password "$ss_pass" \
  --argjson routeVersion "$version" \
  '{deviceId:$deviceId,listenPort:$listenPort,method:$method,password:$password,routeVersion:$routeVersion}'
REMOTE
)"
configure_command="${configure_command//__DEVICE_ID__/$device_id}"
configure_command="${configure_command//__INGRESS_PORT__/$ingress_port}"
configure_command="${configure_command//__METHOD__/$method}"
configure_command="${configure_command//__UPSTREAM_PORT__/$upstream_port}"
configure_command="${configure_command//__UPSTREAM_HOST__/$upstream_host}"
configure_command="${configure_command//__UPSTREAM_USER__/$upstream_user}"
configure_command="${configure_command//__UPSTREAM_PASS__/$upstream_pass}"

echo "creating/updating canary device and route through private Control API" >&2
configure_output="$(run_command "$configure_command" "$control_instance_id")"
canary_json="$(printf '%s\n' "$configure_output" | jq -Rc 'fromjson? | select(type == "object" and has("password"))' | tail -n 1)"
if [ -z "$canary_json" ]; then
  echo "canary configuration did not return the expected JSON payload" >&2
  printf '%s\n' "$configure_output" | sed -E 's/"password":"[^"]+"/"password":"<redacted>"/g' >&2
  exit 1
fi
password="$(jq -er '.password' <<<"$canary_json")"
route_version="$(jq -er '.routeVersion' <<<"$canary_json")"

check_dir="$(mktemp -d)"
client_port="$((19000 + RANDOM % 1000))"
client_name="proxymesh-canary-singbox-$$"
cleanup_client() {
  "$docker_bin" rm -f "$client_name" >/dev/null 2>&1 || true
}
trap 'cleanup_client; rm -rf "$check_dir"; cleanup' EXIT
jq -n \
  --arg server "$nlb_dns" \
  --argjson serverPort "$ingress_port" \
  --arg method "$method" \
  --arg password "$password" \
  '{
    log:{level:"warn"},
    inbounds:[{type:"mixed",tag:"mixed-in",listen:"0.0.0.0",listen_port:19180}],
    outbounds:[{type:"shadowsocks",tag:"ss-out",server:$server,server_port:$serverPort,method:$method,password:$password}],
    route:{rules:[{inbound:["mixed-in"],outbound:"ss-out"}],final:"ss-out"}
  }' >"$check_dir/sing-box.json"
jq -e '.outbounds[0].password | type == "string" and length > 0' "$check_dir/sing-box.json" >/dev/null
"$docker_bin" run -d --name "$client_name" \
  -p "127.0.0.1:${client_port}:19180" \
  -v "$check_dir/sing-box.json:/etc/sing-box/config.json:ro" \
  "$sing_box_image" run -c /etc/sing-box/config.json >/dev/null
echo "checking public NLB through temporary local sing-box client" >&2
for _ in {1..30}; do
  if curl -fsS --socks5-hostname "127.0.0.1:${client_port}" http://example.com/ >/dev/null; then
    break
  fi
  sleep 1
done
curl -fsS --socks5-hostname "127.0.0.1:${client_port}" http://example.com/ >/dev/null || {
  jq '(.outbounds[0].password) = "<redacted>"' "$check_dir/sing-box.json" >&2 || true
  "$docker_bin" logs "$client_name" >&2 || true
  exit 1
}

rotate_command="$(cat <<'REMOTE'
set -eu
device_id="__DEVICE_ID__"
token="$(sed -n 's/^ADMIN_TOKEN=//p' /run/proxymesh/control.env)"
curl -fsS -o /dev/null \
  -X POST "http://127.0.0.1:8080/v1/devices/$device_id/credentials/rotate" \
  -H "Authorization: Bearer $token"
echo "canary device password rotated"
REMOTE
)"
rotate_command="${rotate_command//__DEVICE_ID__/$device_id}"
if [ "$rotate_after" = "true" ]; then
  run_command "$rotate_command" "$control_instance_id" >/dev/null
fi

jq -nc \
  --arg deviceId "$device_id" \
  --arg nlb "$nlb_dns" \
  --argjson listenPort "$ingress_port" \
  --argjson routeVersion "$route_version" \
  --argjson gateways "${#gateway_instance_ids[@]}" \
  --argjson rotatedAfter "$([ "$rotate_after" = "true" ] && printf true || printf false)" \
  '{dataPlane:"ok",deviceId:$deviceId,nlb:$nlb,listenPort:$listenPort,routeVersion:$routeVersion,gateways:$gateways,rotatedAfter:$rotatedAfter}'
