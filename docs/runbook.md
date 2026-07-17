# Operations runbook

## Health check the deployed Alibaba stack

From an operator machine with Alibaba Cloud CLI access:

```bash
REGION=cn-hongkong ALIYUN_PROFILE=hz ALIYUN_BIN=/usr/local/bin/aliyun \
  scripts/aliyun-control-smoke.sh proxymesh-staging
```

Expected output:

```json
{"control":"live","gateways":{"total":2,"ready":2}}
```

The script runs only non-mutating checks on the Control ECS. It deliberately
does not accept request bodies, device passwords, or SOCKS5 credentials.

## Verify the public data plane

Run a full staging canary from an operator machine with Docker and Alibaba Cloud
CLI access:

```bash
REGION=cn-hongkong ALIYUN_PROFILE=hz ALIYUN_BIN=/usr/local/bin/aliyun \
  DOCKER_BIN=/usr/local/bin/docker \
  scripts/aliyun-data-plane-canary.sh proxymesh-staging
```

Expected output includes:

```json
{"dataPlane":"ok","gateways":2}
```

The script starts temporary authenticated SOCKS5 servers on the Gateway ECS
instances, creates or rotates a disposable Shadowsocks canary device through the
private Control API, then launches a local sing-box client and curls
`example.com` through the public NLB. It removes the temporary SOCKS5 processes
at exit.

## Onboard a real device

1. Ensure the device-facing DNS name points at the public Gateway NLB.
2. Connect to the private Control API through VPN, bastion, or a self-hosted
   runner inside the VPC.
3. Create/import the device Shadowsocks ingress. For existing sing-box configs,
   use `scripts/import-singbox-device.sh` from inside that private network.
4. Assign the upstream SOCKS5 route with the current `expectedVersion`.
   Store the request body in a local `0600` file and send it through the
   private Control API:

   ```bash
   umask 077
   printf '%s\n' \
     '{"host":"socks.example.net","port":1080,"username":"user","password":"secret"}' \
     >/private/tmp/device-001-route.json
   DEVICE_ID=device-001 CONTROL_URL=http://control-private:8080 ADMIN_TOKEN=... \
     scripts/put-device-route.sh 0 /private/tmp/device-001-route.json
   ```

5. Confirm `GET /v1/devices/{deviceId}/status` shows the active route version.
6. Restart or reload the local device's sing-box and test one new TCP request.

Keep provider SOCKS5 passwords out of Cloud Assistant command content and CI
logs. The Control API response returns device Shadowsocks passwords only at
creation/rotation time; store the returned sing-box snippet in the device
configuration system immediately.

## A Gateway is stuck

1. Call `POST /v1/gateways/{gatewayId}/draining` with `{"draining":true}`. The Gateway acknowledges, `/ready:18080` becomes 503, and the node leaves route-publication quorum.
2. Inspect heartbeat age, process state, file descriptors, active connections, and canary failures in SLS/CloudMonitor.
3. Keep the node out of publication quorum by completing drain/disconnect, then retry the route update.
4. ESS replaces the instance. A replacement receives no NLB traffic until snapshot sync and canary succeed.

## Route switch

Read the current route version, then submit the new credential with that `expectedVersion`. A 409 means another operator already changed it. A 503 means at least one Gateway failed to acknowledge; drain or repair that Gateway and retry with the still-current version. Password fields are never returned by status APIs.

## Scale out and in

Scale out through the manual GitHub workflow or ROS parameter. New instances complete the Pending Add hook only after readiness. Scale in first sets DRAINING, waits for active connections to reach zero, and forcibly continues after ten minutes. Staging may run two Gateways for validation; production keeps at least four, and rolling replacement adds a ready spare before draining an old node.

Automatic scale-out should alert on active connections, network bandwidth, file-descriptor utilization, and CPU. Automatic scale-in is disabled for phase one.

## Migrate a device to Shadowsocks 2022

Treat the ingress method and PSK as a device credential change, not as an
upstream SOCKS5 route change. Add a parallel ingress and let Control Plane
generate the Base64 16-byte PSK:

```bash
curl -sS -X POST http://127.0.0.1:8080/v1/devices/device-001/ingresses \
  -H 'Authorization: Bearer local-admin-token' \
  -H 'Content-Type: application/json' \
  -d '{"listenPort":50001,"method":"2022-blake3-aes-128-gcm"}'
```

The response returns the password and sing-box outbound once. The new ingress
automatically shares the device's active SOCKS5 route; route changes apply to
both old and new ports. Update the device's sing-box configuration and verify
new connections, then retire the legacy port:

```bash
curl -sS -X DELETE \
  http://127.0.0.1:8080/v1/devices/device-001/ingresses/50000 \
  -H 'Authorization: Bearer local-admin-token'
```

Control Plane refuses to delete the last ingress. Other devices and existing
connections are unaffected; deleting an ingress prevents new connections on
that port but does not interrupt already established relays.

Do not reuse an ordinary legacy password as an SS2022 key. Do not deploy GOST
3.2.6 as the SS2022 terminator: the compatibility test found response-direction
authentication failures. Gateway pins the tested GOST protocol library instead.
Run `tests/integration/ss2022/run.sh` before releasing a dependency upgrade.
