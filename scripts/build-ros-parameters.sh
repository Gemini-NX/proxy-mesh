#!/usr/bin/env bash
set -euo pipefail

if [ "$#" -ne 1 ]; then
  echo "usage: $0 OUTPUT_JSON" >&2
  exit 2
fi

required=(ENVIRONMENT_NAME ZONE_ID_A ZONE_ID_B EXISTING_VPC_ID EXISTING_VPC_CIDR VSWITCH_ID_A VSWITCH_ID_B VSWITCH_CIDR_A VSWITCH_CIDR_B ECS_IMAGE_ID GATEWAY_IMAGE CONTROL_IMAGE GHCR_USERNAME GHCR_TOKEN DB_PASSWORD RUNTIME_SECRET_DATA)
for name in "${required[@]}"; do
  [ -n "${!name:-}" ] || { echo "$name is required" >&2; exit 1; }
done

printf '%s' "$RUNTIME_SECRET_DATA" | jq -e '
  type == "object" and
  (["encryptionKey","adminToken","publicProxyHost","canaryDeviceId","controlTLSServerName","controlServerCA","controlServerCert","controlServerKey","gatewayClientCA","gatewayClientCert","gatewayClientKey"] - keys | length == 0) and
  (.encryptionKey | type == "string" and length >= 32) and
  (.adminToken | type == "string" and length >= 32 and contains("\n") | not) and
  (.publicProxyHost | type == "string" and length > 0)
' >/dev/null || { echo "RUNTIME_SECRET_DATA is incomplete or invalid" >&2; exit 1; }
[ "${#DB_PASSWORD}" -ge 16 ] || { echo "DB_PASSWORD must contain at least 16 characters" >&2; exit 1; }
[ "${#DB_PASSWORD}" -le 32 ] || { echo "DB_PASSWORD must contain at most 32 characters" >&2; exit 1; }
[ "${#GHCR_USERNAME}" -ge 1 ] || { echo "GHCR_USERNAME is required" >&2; exit 1; }
[ "${#GHCR_TOKEN}" -ge 20 ] || { echo "GHCR_TOKEN does not look like a GitHub token" >&2; exit 1; }
case "$GHCR_TOKEN" in *$'\n'*) echo "GHCR_TOKEN must not contain a newline" >&2; exit 1;; esac

gateway_user_data="$(scripts/render-cloud-init.sh "$GATEWAY_IMAGE")"
control_user_data="$(scripts/render-control-cloud-init.sh "$CONTROL_IMAGE")"
gateway_runtime="$(printf '%s' "$RUNTIME_SECRET_DATA" | jq -c '{encryptionKey,canaryDeviceId,controlTLSServerName,controlServerCA,gatewayClientCert,gatewayClientKey}')"
control_runtime="$(printf '%s' "$RUNTIME_SECRET_DATA" | jq -c --arg databasePassword "$DB_PASSWORD" '{encryptionKey,adminToken,publicProxyHost,controlServerCert,controlServerKey,gatewayClientCA,databasePassword:$databasePassword}')"
registry_secret="$(jq -nc --arg username "$GHCR_USERNAME" --arg token "$GHCR_TOKEN" '{username:$username,token:$token}')"
gateway_runtime_b64="$(printf '%s' "$gateway_runtime" | base64 | tr -d '\n')"
control_runtime_b64="$(printf '%s' "$control_runtime" | base64 | tr -d '\n')"
registry_secret_b64="$(printf '%s' "$registry_secret" | base64 | tr -d '\n')"
umask 077
jq -n \
  --arg environment "$ENVIRONMENT_NAME" \
  --arg zoneA "$ZONE_ID_A" --arg zoneB "$ZONE_ID_B" \
  --arg vpc "$EXISTING_VPC_ID" --arg vpcCidr "$EXISTING_VPC_CIDR" \
  --arg vswitchA "$VSWITCH_ID_A" --arg vswitchB "$VSWITCH_ID_B" \
  --arg vswitchCidrA "$VSWITCH_CIDR_A" --arg vswitchCidrB "$VSWITCH_CIDR_B" \
  --arg ecsImage "$ECS_IMAGE_ID" --arg ecsType "${ECS_INSTANCE_TYPE:-ecs.c7.large}" \
  --arg gatewayImage "$GATEWAY_IMAGE" --arg controlImage "$CONTROL_IMAGE" \
  --arg gatewayUserData "$gateway_user_data" --arg controlUserData "$control_user_data" \
  --arg sourceCidr "${DEVICE_SOURCE_CIDR:-0.0.0.0/0}" --arg dbPassword "$DB_PASSWORD" \
  --arg gatewayRuntimeB64 "$gateway_runtime_b64" --arg controlRuntimeB64 "$control_runtime_b64" \
  --arg registrySecretB64 "$registry_secret_b64" \
  --arg desired "${GATEWAY_DESIRED_CAPACITY:-2}" \
  '[
    {ParameterKey:"EnvironmentName",ParameterValue:$environment},
    {ParameterKey:"ZoneIdA",ParameterValue:$zoneA},
    {ParameterKey:"ZoneIdB",ParameterValue:$zoneB},
    {ParameterKey:"ExistingVpcId",ParameterValue:$vpc},
    {ParameterKey:"ExistingVpcCidr",ParameterValue:$vpcCidr},
    {ParameterKey:"ExistingVSwitchIdA",ParameterValue:$vswitchA},
    {ParameterKey:"ExistingVSwitchIdB",ParameterValue:$vswitchB},
    {ParameterKey:"ExistingVSwitchCidrA",ParameterValue:$vswitchCidrA},
    {ParameterKey:"ExistingVSwitchCidrB",ParameterValue:$vswitchCidrB},
    {ParameterKey:"ECSImageId",ParameterValue:$ecsImage},
    {ParameterKey:"ECSInstanceType",ParameterValue:$ecsType},
    {ParameterKey:"GatewayImage",ParameterValue:$gatewayImage},
    {ParameterKey:"ControlImage",ParameterValue:$controlImage},
    {ParameterKey:"GatewayUserData",ParameterValue:$gatewayUserData},
    {ParameterKey:"ControlUserData",ParameterValue:$controlUserData},
    {ParameterKey:"DeviceSourceCidr",ParameterValue:$sourceCidr},
    {ParameterKey:"GatewayDesiredCapacity",ParameterValue:$desired},
    {ParameterKey:"EnableAutoScaleOut",ParameterValue:"false"},
    {ParameterKey:"RequireCanary",ParameterValue:"false"},
    {ParameterKey:"DBPassword",ParameterValue:$dbPassword},
    {ParameterKey:"GatewayRuntimeSecretB64",ParameterValue:$gatewayRuntimeB64},
    {ParameterKey:"ControlRuntimeSecretB64",ParameterValue:$controlRuntimeB64},
    {ParameterKey:"RegistrySecretB64",ParameterValue:$registrySecretB64}
  ]' > "$1"
chmod 600 "$1"
echo "wrote protected ROS parameters to $1"
