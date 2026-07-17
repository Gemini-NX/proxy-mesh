#!/usr/bin/env bash
set -euo pipefail

: "${REGION:?REGION is required}"
if [ "$#" -ne 1 ]; then
  echo "usage: $0 STACK_ID" >&2
  exit 2
fi

stack_id="$1"
profile="${ALIYUN_PROFILE:-default}"
aliyun_bin="${ALIYUN_BIN:-aliyun}"
for _ in {1..180}; do
  response="$("$aliyun_bin" ros GetStack --profile "$profile" --RegionId "$REGION" --StackId "$stack_id" --OutputOption Disabled)"
  status="$(jq -r '.Status' <<<"$response")"
  case "$status" in
    CREATE_COMPLETE|UPDATE_COMPLETE) exit 0 ;;
    *_FAILED|*_ROLLBACK_*|DELETE_*)
      echo "ROS stack $stack_id entered $status" >&2
      exit 1
      ;;
  esac
  sleep 10
done
echo "timed out waiting for ROS stack $stack_id" >&2
exit 1
