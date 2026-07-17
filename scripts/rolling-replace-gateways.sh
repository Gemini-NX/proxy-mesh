#!/usr/bin/env bash
set -euo pipefail

: "${REGION:?REGION is required}"
: "${GATEWAY_SCALING_GROUP_ID:?GATEWAY_SCALING_GROUP_ID is required}"
minimum="${MIN_GATEWAYS:-4}"

group="$(aliyun ess DescribeScalingGroups --RegionId "$REGION" --ScalingGroupId.1 "$GATEWAY_SCALING_GROUP_ID")"
desired="$(jq -r '.ScalingGroups.ScalingGroup[0].DesiredCapacity' <<<"$group")"
maximum="$(jq -r '.ScalingGroups.ScalingGroup[0].MaxSize' <<<"$group")"
[ "$desired" -ge "$minimum" ] || { echo "desired capacity is below the safety floor ($minimum)" >&2; exit 1; }
[ "$desired" -lt "$maximum" ] || { echo "rolling replacement needs one spare capacity slot" >&2; exit 1; }

instances="$(aliyun ess DescribeScalingInstances --RegionId "$REGION" --ScalingGroupId "$GATEWAY_SCALING_GROUP_ID")"
mapfile -t old_ids < <(jq -r '.ScalingInstances.ScalingInstance[] | select(.LifecycleState == "InService") | .InstanceId' <<<"$instances")
[ "${#old_ids[@]}" -ge "$minimum" ] || { echo "fewer than $minimum in-service gateways" >&2; exit 1; }

wait_for_count() {
  local expected="$1"
  for _ in {1..180}; do
    local state count
    state="$(aliyun ess DescribeScalingInstances --RegionId "$REGION" --ScalingGroupId "$GATEWAY_SCALING_GROUP_ID")"
    count="$(jq '[.ScalingInstances.ScalingInstance[] | select(.LifecycleState == "InService")] | length' <<<"$state")"
    [ "$count" -ge "$expected" ] && return 0
    sleep 10
  done
  echo "timed out waiting for $expected in-service gateways" >&2
  return 1
}

for instance_id in "${old_ids[@]}"; do
  # Add and fully ready the replacement before the old instance enters drain.
  aliyun ess ModifyScalingGroup \
    --RegionId "$REGION" \
    --ScalingGroupId "$GATEWAY_SCALING_GROUP_ID" \
    --DesiredCapacity "$((desired + 1))"
  wait_for_count "$((desired + 1))"

  aliyun ess RemoveInstances \
    --RegionId "$REGION" \
    --ScalingGroupId "$GATEWAY_SCALING_GROUP_ID" \
    --InstanceId.1 "$instance_id" \
    --DecreaseDesiredCapacity true \
    --RemovePolicy release \
    --ClientToken "${GITHUB_RUN_ID:-manual}-${instance_id}"
  wait_for_count "$desired"
done
