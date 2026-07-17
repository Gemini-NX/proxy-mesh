#!/usr/bin/env bash
set -euo pipefail

if [ "$#" -ne 1 ]; then
  echo "usage: $0 OUTPUT_JSON" >&2
  exit 2
fi

required=(ENVIRONMENT_NAME ZONE_ID_A ZONE_ID_B ECS_IMAGE_ID GATEWAY_IMAGE CONTROL_IMAGE DB_PASSWORD RUNTIME_SECRET_DATA DEVICE_SOURCE_CIDR)
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

gateway_user_data="$(scripts/render-cloud-init.sh "$GATEWAY_IMAGE")"
control_user_data="$(scripts/render-control-cloud-init.sh "$CONTROL_IMAGE")"
gateway_runtime="$(printf '%s' "$RUNTIME_SECRET_DATA" | jq -c '{encryptionKey,canaryDeviceId,controlTLSServerName,controlServerCA,gatewayClientCert,gatewayClientKey}')"
control_runtime="$(printf '%s' "$RUNTIME_SECRET_DATA" | jq -c '{encryptionKey,adminToken,publicProxyHost,controlServerCert,controlServerKey,gatewayClientCA}')"
umask 077
jq -n \
  --arg environment "$ENVIRONMENT_NAME" \
  --arg zoneA "$ZONE_ID_A" --arg zoneB "$ZONE_ID_B" \
  --arg ecsImage "$ECS_IMAGE_ID" --arg ecsType "${ECS_INSTANCE_TYPE:-ecs.c7.large}" \
  --arg gatewayImage "$GATEWAY_IMAGE" --arg controlImage "$CONTROL_IMAGE" \
  --arg gatewayUserData "$gateway_user_data" --arg controlUserData "$control_user_data" \
  --arg sourceCidr "$DEVICE_SOURCE_CIDR" --arg dbPassword "$DB_PASSWORD" \
  --arg gatewayRuntime "$gateway_runtime" --arg controlRuntime "$control_runtime" \
  --arg desired "${GATEWAY_DESIRED_CAPACITY:-4}" \
  '[
    {ParameterKey:"EnvironmentName",ParameterValue:$environment},
    {ParameterKey:"ZoneIdA",ParameterValue:$zoneA},
    {ParameterKey:"ZoneIdB",ParameterValue:$zoneB},
    {ParameterKey:"ECSImageId",ParameterValue:$ecsImage},
    {ParameterKey:"ECSInstanceType",ParameterValue:$ecsType},
    {ParameterKey:"GatewayImage",ParameterValue:$gatewayImage},
    {ParameterKey:"ControlImage",ParameterValue:$controlImage},
    {ParameterKey:"GatewayUserData",ParameterValue:$gatewayUserData},
    {ParameterKey:"ControlUserData",ParameterValue:$controlUserData},
    {ParameterKey:"DeviceSourceCidr",ParameterValue:$sourceCidr},
    {ParameterKey:"GatewayDesiredCapacity",ParameterValue:$desired},
    {ParameterKey:"RequireCanary",ParameterValue:"false"},
    {ParameterKey:"DBPassword",ParameterValue:$dbPassword},
    {ParameterKey:"GatewayRuntimeSecretData",ParameterValue:$gatewayRuntime},
    {ParameterKey:"ControlRuntimeSecretData",ParameterValue:$controlRuntime}
  ]' > "$1"
chmod 600 "$1"
echo "wrote protected ROS parameters to $1"
