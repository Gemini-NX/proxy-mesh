#!/usr/bin/env bash
set -euo pipefail

if [ "$#" -ne 3 ]; then
  echo "usage: $0 STACK_NAME CHANGE_SET_NAME PARAMETERS_JSON" >&2
  exit 2
fi

stack_name="$1"
change_set_name="$2"
parameters_file="$3"
region="${ALIYUN_REGION:-cn-hongkong}"
profile="${ALIYUN_PROFILE:-default}"
aliyun_bin="${ALIYUN_BIN:-aliyun}"
template_file="${ROS_TEMPLATE_FILE:-infra/ros/main.yaml}"
stack_policy_file="${ROS_STACK_POLICY_FILE:-infra/ros/stack-policy.json}"

for required_file in "$parameters_file" "$template_file" "$stack_policy_file"; do
  [ -r "$required_file" ] || { echo "cannot read $required_file" >&2; exit 1; }
done
jq -e 'type == "array" and length > 0 and all(.[]; (.ParameterKey | type == "string") and (.ParameterValue | type == "string"))' "$parameters_file" >/dev/null

cli_args=(
  ros CreateChangeSet
  --profile "$profile"
  --RegionId "$region"
  --StackName "$stack_name"
  --ChangeSetName "$change_set_name"
  --ChangeSetType CREATE
  --DisableRollback false
  --Description "ProxyMesh two-gateway staging validation"
  --TemplateBody "$(<"$template_file")"
  --StackPolicyBody "$(<"$stack_policy_file")"
)

parameter_index=1
while IFS= read -r parameter; do
  parameter_key="$(jq -er '.ParameterKey' <<<"$parameter")"
  parameter_value="$(jq -er '.ParameterValue' <<<"$parameter")"
  cli_args+=(
    "--Parameters.${parameter_index}.ParameterKey" "$parameter_key"
    "--Parameters.${parameter_index}.ParameterValue" "$parameter_value"
  )
  parameter_index=$((parameter_index + 1))
done < <(jq -c '.[]' "$parameters_file")

"$aliyun_bin" "${cli_args[@]}"
