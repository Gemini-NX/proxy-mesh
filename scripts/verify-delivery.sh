#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat >&2 <<'USAGE'
usage: scripts/verify-delivery.sh STACK_ID_OR_NAME

Runs the delivery acceptance suite for the current ProxyMesh workspace and the
Alibaba Cloud staging stack:
  - shell syntax checks for operator scripts
  - go test ./...
  - docker compose config --quiet
  - ROS ValidateTemplate
  - private Control/Gateway smoke
  - public Shadowsocks data-plane canary
  - DNS CNAME advisory for the device-facing hostname

Environment:
  REGION=cn-hongkong
  ALIYUN_PROFILE=hz
  ALIYUN_BIN=aliyun
  GO_BIN=go
  DOCKER_BIN=docker
  DEVICE_DNS_NAME=proxy-mesh.lintan-mob.com
  SKIP_DATA_PLANE_CANARY=false
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
go_bin="${GO_BIN:-go}"
docker_bin="${DOCKER_BIN:-docker}"
device_dns="${DEVICE_DNS_NAME:-proxy-mesh.lintan-mob.com}"

echo "== script syntax =="
bash -n scripts/*.sh

echo "== go test =="
mkdir -p /private/tmp/proxymesh-gocache
GOCACHE=/private/tmp/proxymesh-gocache "$go_bin" test ./...

echo "== docker compose config =="
"$docker_bin" compose config --quiet

echo "== ROS template validation =="
python3 -c 'import json,pathlib,yaml; data=yaml.safe_load(pathlib.Path("infra/ros/main.yaml").read_text()); pathlib.Path("/private/tmp/proxymesh-ros-template.json").write_text(json.dumps(data,separators=(",",":")))'
"$aliyun_bin" ros ValidateTemplate \
  --profile "$profile" \
  --RegionId "$region" \
  --TemplateBody "$(cat /private/tmp/proxymesh-ros-template.json)" >/dev/null

echo "== control smoke =="
REGION="$region" ALIYUN_PROFILE="$profile" ALIYUN_BIN="$aliyun_bin" \
  scripts/aliyun-control-smoke.sh "$stack_ref"

if [ "${SKIP_DATA_PLANE_CANARY:-false}" != "true" ]; then
  echo "== public data-plane canary =="
  REGION="$region" ALIYUN_PROFILE="$profile" ALIYUN_BIN="$aliyun_bin" DOCKER_BIN="$docker_bin" \
    scripts/aliyun-data-plane-canary.sh "$stack_ref"
fi

echo "== DNS advisory =="
stack_id="$(
  "$aliyun_bin" ros ListStacks --profile "$profile" --RegionId "$region" \
    --StackName.1 "$stack_ref" \
    --Status.1 CREATE_COMPLETE \
    --Status.2 UPDATE_COMPLETE \
    --Status.3 ROLLBACK_COMPLETE |
    jq -er --arg name "$stack_ref" '[.Stacks[] | select(.StackName == $name)] | sort_by(.CreateTime) | last.StackId'
)"
nlb_dns="$("$aliyun_bin" ros GetStack --profile "$profile" --RegionId "$region" --StackId "$stack_id" |
  jq -er '.Outputs[] | select(.OutputKey == "NLBDNSName") | .OutputValue')"
resolved="$(dig +short "$device_dns" CNAME | sed 's/\.$//' | tail -n 1 || true)"
if [ "$resolved" = "$nlb_dns" ]; then
  echo "dns ok: $device_dns -> $nlb_dns"
else
  echo "dns pending: set $device_dns CNAME $nlb_dns" >&2
fi

echo "delivery verification passed"
