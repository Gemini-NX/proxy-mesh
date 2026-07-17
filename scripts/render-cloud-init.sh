#!/usr/bin/env sh
set -eu

if [ "$#" -ne 3 ]; then
  echo "usage: $0 GATEWAY_IMAGE CONTROL_GRPC_ADDR KMS_SECRET_NAME" >&2
  exit 2
fi

escape_sed() { printf '%s' "$1" | sed 's/[&|]/\\&/g'; }
image="$(escape_sed "$1")"
control="$(escape_sed "$2")"
secret="$(escape_sed "$3")"

sed \
  -e "s|__GATEWAY_IMAGE__|$image|g" \
  -e "s|__CONTROL_GRPC_ADDR__|$control|g" \
  -e "s|__KMS_SECRET_NAME__|$secret|g" \
  infra/cloud-init/gateway.yaml
