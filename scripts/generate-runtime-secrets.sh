#!/usr/bin/env bash
set -euo pipefail

if [ "$#" -ne 4 ]; then
  echo "usage: $0 OUTPUT_DIR CONTROL_TLS_NAME PUBLIC_PROXY_HOST CANARY_DEVICE_ID" >&2
  exit 2
fi

out="$1"
[ ! -e "$out" ] || { echo "refusing to overwrite $out" >&2; exit 1; }
umask 077
mkdir -p "$out"

openssl genpkey -algorithm EC -pkeyopt ec_paramgen_curve:P-256 -out "$out/ca-key.pem"
openssl req -x509 -new -sha256 -days 3650 -key "$out/ca-key.pem" \
  -subj '/CN=ProxyMesh private CA' -out "$out/ca.pem"

openssl genpkey -algorithm EC -pkeyopt ec_paramgen_curve:P-256 -out "$out/control-server-key.pem"
openssl req -new -sha256 -key "$out/control-server-key.pem" \
  -subj "/CN=$2" -out "$out/control-server.csr"
printf '%s\n' 'basicConstraints=critical,CA:FALSE' 'keyUsage=critical,digitalSignature,keyAgreement' \
  'extendedKeyUsage=serverAuth' "subjectAltName=DNS:$2" > "$out/control-server.ext"
openssl x509 -req -sha256 -days 825 -in "$out/control-server.csr" \
  -CA "$out/ca.pem" -CAkey "$out/ca-key.pem" -CAcreateserial \
  -extfile "$out/control-server.ext" -out "$out/control-server.pem"

openssl genpkey -algorithm EC -pkeyopt ec_paramgen_curve:P-256 -out "$out/gateway-client-key.pem"
openssl req -new -sha256 -key "$out/gateway-client-key.pem" \
  -subj '/CN=proxymesh-gateways' -out "$out/gateway-client.csr"
printf '%s\n' 'basicConstraints=critical,CA:FALSE' 'keyUsage=critical,digitalSignature,keyAgreement' \
  'extendedKeyUsage=clientAuth' > "$out/gateway-client.ext"
openssl x509 -req -sha256 -days 825 -in "$out/gateway-client.csr" \
  -CA "$out/ca.pem" -CAkey "$out/ca-key.pem" -CAcreateserial \
  -extfile "$out/gateway-client.ext" -out "$out/gateway-client.pem"

encryption_key="$(openssl rand -base64 32)"
admin_token="$(openssl rand -base64 48 | tr -d '\n')"
jq -n \
  --arg encryptionKey "$encryption_key" --arg adminToken "$admin_token" \
  --arg publicProxyHost "$3" --arg canaryDeviceId "$4" --arg controlTLSServerName "$2" \
  --rawfile ca "$out/ca.pem" --rawfile serverCert "$out/control-server.pem" \
  --rawfile serverKey "$out/control-server-key.pem" \
  --rawfile clientCert "$out/gateway-client.pem" --rawfile clientKey "$out/gateway-client-key.pem" \
  '{encryptionKey:$encryptionKey,adminToken:$adminToken,publicProxyHost:$publicProxyHost,canaryDeviceId:$canaryDeviceId,controlTLSServerName:$controlTLSServerName,controlServerCA:$ca,controlServerCert:$serverCert,controlServerKey:$serverKey,gatewayClientCA:$ca,gatewayClientCert:$clientCert,gatewayClientKey:$clientKey}' \
  > "$out/runtime-secret.json"

rm -f "$out"/*.csr "$out"/*.ext "$out"/*.srl
echo "created protected runtime material in $out"
echo "keep ca-key.pem offline; RUNTIME_SECRET_DATA is $out/runtime-secret.json"
