#!/usr/bin/env bash
set -euo pipefail

: "${REGION:?REGION is required}"
if [ "$#" -ne 1 ]; then
  echo "usage: $0 STACK_ID_OR_NAME" >&2
  exit 2
fi

stack_ref="$1"
profile="${ALIYUN_PROFILE:-default}"
aliyun_bin="${ALIYUN_BIN:-aliyun}"

resolve_stack_id() {
  case "$stack_ref" in
    *-*-*-*-*) printf '%s\n' "$stack_ref"; return 0 ;;
  esac
  "$aliyun_bin" ros ListStacks --profile "$profile" --RegionId "$REGION" \
    --StackName.1 "$stack_ref" \
    --Status.1 CREATE_IN_PROGRESS \
    --Status.2 CREATE_COMPLETE \
    --Status.3 UPDATE_IN_PROGRESS \
    --Status.4 UPDATE_COMPLETE \
    --Status.5 ROLLBACK_IN_PROGRESS \
    --Status.6 ROLLBACK_COMPLETE \
    --Status.7 CREATE_FAILED \
    --Status.8 UPDATE_FAILED |
    jq -er --arg name "$stack_ref" '
      [.Stacks[] | select(.StackName == $name)] |
      sort_by(.CreateTime) |
      last.StackId
    '
}

stack_id="$(resolve_stack_id)"
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
