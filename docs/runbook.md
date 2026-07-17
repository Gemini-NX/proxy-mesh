# Operations runbook

## A Gateway is stuck

1. Call `POST /v1/gateways/{gatewayId}/draining` with `{"draining":true}`. The Gateway acknowledges, `/ready:18080` becomes 503, and the node leaves route-publication quorum.
2. Inspect heartbeat age, process state, file descriptors, active connections, and canary failures in SLS/CloudMonitor.
3. Keep the node out of publication quorum by completing drain/disconnect, then retry the route update.
4. ESS replaces the instance. A replacement receives no NLB traffic until snapshot sync and canary succeed.

## Route switch

Read the current route version, then submit the new credential with that `expectedVersion`. A 409 means another operator already changed it. A 503 means at least one Gateway failed to acknowledge; drain or repair that Gateway and retry with the still-current version. Password fields are never returned by status APIs.

## Scale out and in

Scale out through the manual GitHub workflow or ROS parameter. New instances complete the Pending Add hook only after readiness. Scale in first sets DRAINING, waits for active connections to reach zero, and forcibly continues after ten minutes. Keep at least three healthy Gateways during releases.

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
