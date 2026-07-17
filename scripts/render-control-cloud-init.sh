#!/usr/bin/env sh
set -eu

if [ "$#" -ne 1 ]; then
  echo "usage: $0 CONTROL_IMAGE" >&2
  exit 2
fi

image="$(printf '%s' "$1" | sed 's/[&|]/\\&/g')"
sed -e "s|__CONTROL_IMAGE__|$image|g" infra/cloud-init/control-plane.yaml
