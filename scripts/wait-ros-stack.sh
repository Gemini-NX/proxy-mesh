#!/usr/bin/env bash
set -euo pipefail

: "${REGION:?REGION is required}"
if [ "$#" -ne 1 ]; then
  echo "usage: $0 STACK_NAME" >&2
  exit 2
fi

stack="$1"
for _ in {1..180}; do
  response="$(aliyun ros GetStack --RegionId "$REGION" --StackName "$stack")"
  status="$(jq -r '.Status' <<<"$response")"
  case "$status" in
    CREATE_COMPLETE|UPDATE_COMPLETE) exit 0 ;;
    *_FAILED|*_ROLLBACK_*|DELETE_*)
      echo "ROS stack $stack entered $status" >&2
      exit 1
      ;;
  esac
  sleep 10
done
echo "timed out waiting for ROS stack $stack" >&2
exit 1
