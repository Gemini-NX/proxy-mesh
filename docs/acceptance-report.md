# ProxyMesh staging acceptance report

Generated for the current handoff state.

## Version

```text
Repository: Gemini-NX/proxy-mesh
Branch: main
Commit: a4f77e99f790eafed2357ce8ddd2b00c00b2ae83
```

## Alibaba Cloud stack

```text
Region: cn-hongkong
Resource Group: wucha_edm-sqd / rg-aeky5chnwj55sta
ROS Stack: proxymesh-staging
ROS Status: UPDATE_COMPLETE
Status reason: Stack successfully updated
```

Outputs:

```text
Gateway public NLB: nlb-o1j1jeu96kcsh5gzm5.cn-hongkong.nlb.aliyuncsslbintl.com
Control private NLB: nlb-6evyw9iljszkc9ltfy.cn-hongkong.nlb.aliyuncsslbintl.com
Control gRPC endpoint: nlb-6evyw9iljszkc9ltfy.cn-hongkong.nlb.aliyuncsslbintl.com:9090
Control ECS: i-j6c4d4qjs5f1j8grzuyy
Gateway ASG: asg-j6c6h0zf9t0bmw6kblxo
PostgreSQL endpoint: pgm-j6c1800gkavvka3m.pg.cnhk.rds.aliyuncs.com
NAT EIP: 47.243.127.214
```

Gateway capacity:

```text
Desired: 2
Active: 2
Pending: 0
Total: 2
Max: 20
```

Gateway instances:

```text
i-j6cg351k2h0nbcikekb3  172.16.0.200    InService  Healthy
i-j6c0yiozukjad0mo7qny  172.16.16.189   InService  Healthy
```

## Acceptance evidence

The delivery suite passed with:

```bash
REGION=cn-hongkong \
ALIYUN_PROFILE=hz \
ALIYUN_BIN=/usr/local/bin/aliyun \
GO_BIN=/opt/homebrew/bin/go \
DOCKER_BIN=/usr/local/bin/docker \
scripts/verify-delivery.sh proxymesh-staging
```

Observed result:

```text
script syntax                         passed
go test ./...                         passed
docker compose config --quiet          passed
ROS ValidateTemplate                   passed
control smoke                          passed
public data-plane canary               passed
delivery verification                  passed
```

Control smoke:

```json
{"control":"live","gateways":{"total":4,"ready":2}}
```

`total` includes stale disconnected sessions until Control Plane ages them out;
`ready=2` matches the active Gateway capacity.

Public data-plane canary:

```json
{"dataPlane":"ok","deviceId":"staging-e2e","nlb":"nlb-o1j1jeu96kcsh5gzm5.cn-hongkong.nlb.aliyuncsslbintl.com","listenPort":59998,"routeVersion":2,"gateways":2,"rotatedAfter":true}
```

This proves:

```text
local sing-box client
-> public NLB
-> Gateway Shadowsocks listener
-> per-device route snapshot
-> authenticated SOCKS5 upstream
-> example.com
```

## Security and operations notes

- Device traffic enters Gateway through Shadowsocks, not HTTPS CONNECT.
- Control API and Control gRPC are private-only.
- SOCKS5 passwords and device Shadowsocks passwords must not be sent through
  ECS RunCommand/OOS command content or CI logs.
- Route updates use CAS `expectedVersion`.
- Existing connections remain until closed or explicitly interrupted.
- Gateway scale-out instances do not receive NLB traffic until lifecycle
  readiness passes.

## External prerequisite still pending

DNS for the device-facing hostname is outside this Alibaba Cloud account and is
not configured yet. Add this record at the authoritative DNS provider:

```text
proxy-mesh.lintan-mob.com CNAME nlb-o1j1jeu96kcsh5gzm5.cn-hongkong.nlb.aliyuncsslbintl.com
```

After adding DNS, rerun `scripts/verify-delivery.sh proxymesh-staging`; the DNS
advisory will change from `dns pending` to `dns ok`.
