#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat >&2 <<'USAGE'
usage: scripts/aliyun-control-smoke.sh STACK_ID_OR_NAME

Runs a non-mutating health check from inside the private Control Plane ECS:
  - GET /live
  - GET /v1/gateways with the node-local ADMIN_TOKEN
  - assert at least MIN_READY_GATEWAYS are READY

No admin token, device password, or SOCKS5 credential is passed through
Cloud Assistant command content.

Environment:
  REGION=cn-hongkong
  ALIYUN_PROFILE=hz
  ALIYUN_BIN=aliyun
  MIN_READY_GATEWAYS=2
  CONTROL_INSTANCE_ID=i-...   # optional; otherwise resolved from ROS outputs
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
minimum="${MIN_READY_GATEWAYS:-2}"

resolve_stack_id() {
  case "$stack_ref" in
    *-*-*-*-*) printf '%s\n' "$stack_ref"; return 0 ;;
  esac
  "$aliyun_bin" ros ListStacks --profile "$profile" --RegionId "$region" \
    --StackName.1 "$stack_ref" \
    --Status.1 CREATE_COMPLETE \
    --Status.2 UPDATE_COMPLETE \
    --Status.3 CREATE_IN_PROGRESS \
    --Status.4 UPDATE_IN_PROGRESS \
    --Status.5 ROLLBACK_COMPLETE |
    jq -er --arg name "$stack_ref" '
      [.Stacks[] | select(.StackName == $name)] |
      sort_by(.CreateTime) |
      last.StackId
    '
}

stack_id="$(resolve_stack_id)"
control_instance_id="${CONTROL_INSTANCE_ID:-}"
if [ -z "$control_instance_id" ]; then
  control_instance_id="$("$aliyun_bin" ros GetStack --profile "$profile" --RegionId "$region" --StackId "$stack_id" |
    jq -er '.Outputs[] | select(.OutputKey == "ControlInstanceId") | .OutputValue')"
fi

remote_command="$(cat <<'REMOTE'
set -eu
token="$(docker inspect proxymesh-control-plane 2>/dev/null | jq -r '.[0].Config.Env[] | select(startswith("ADMIN_TOKEN=")) | sub("^ADMIN_TOKEN=";"")' | tail -n 1)"
if [ -z "$token" ]; then
  token="$(sed -n 's/^ADMIN_TOKEN=//p' /run/proxymesh/control.env)"
fi
test -n "$token"
curl -fsS http://127.0.0.1:8080/live >/dev/null
gateways="$(curl -fsS -H "Authorization: Bearer $token" http://127.0.0.1:8080/v1/gateways)"
ready="$(printf '%s' "$gateways" | jq '[.[] | select(.status == "READY")] | length')"
total="$(printf '%s' "$gateways" | jq 'length')"
minimum="__MIN_READY_GATEWAYS__"
if [ "$total" -lt "$minimum" ]; then
  echo "fewer than $minimum gateways registered: total=$total ready=$ready" >&2
  exit 1
fi
if [ "$ready" -lt "$minimum" ]; then
  echo "fewer than $minimum gateways ready: total=$total ready=$ready" >&2
  exit 1
fi
jq -nc --argjson total "$total" --argjson ready "$ready" \
  '{control:"live", gateways:{total:$total, ready:$ready}}'
REMOTE
)"
remote_command="${remote_command/__MIN_READY_GATEWAYS__/$minimum}"

command_id="$("$aliyun_bin" ecs RunCommand \
  --profile "$profile" \
  --RegionId "$region" \
  --Type RunShellScript \
  --CommandContent "$remote_command" \
  --InstanceId.1 "$control_instance_id" |
  jq -er '.CommandId')"

for _ in {1..60}; do
  result="$("$aliyun_bin" ecs DescribeInvocationResults \
    --profile "$profile" \
    --RegionId "$region" \
    --CommandId "$command_id" \
    --InstanceId "$control_instance_id")"
  status="$(jq -r '.Invocation.InvocationResults.InvocationResult[0].InvocationStatus // "Pending"' <<<"$result")"
  case "$status" in
    Success|Failed|Stopped) break ;;
  esac
  sleep 2
done

exit_code="$(jq -r '.Invocation.InvocationResults.InvocationResult[0].ExitCode // 255' <<<"$result")"
output="$(jq -r '.Invocation.InvocationResults.InvocationResult[0].Output // "" | @base64d' <<<"$result")"
if [ "$exit_code" != "0" ]; then
  printf '%s\n' "$output" >&2
  exit "$exit_code"
fi
printf '%s\n' "$output"
