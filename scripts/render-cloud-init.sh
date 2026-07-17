#!/usr/bin/env sh
set -eu

if [ "$#" -ne 1 ]; then
  echo "usage: $0 GATEWAY_IMAGE" >&2
  exit 2
fi

escape_sed() { printf '%s' "$1" | sed 's/[&|]/\\&/g'; }
image="$(escape_sed "$1")"

sed \
  -e "s|__GATEWAY_IMAGE__|$image|g" \
  infra/cloud-init/gateway.yaml
