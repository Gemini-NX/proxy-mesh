# ProxyMesh

ProxyMesh is a TCP-only Shadowsocks gateway mesh. Existing devices can keep their
`aes-256-gcm` password and dedicated ingress port, while new or migrated devices
can use `2022-blake3-aes-128-gcm`; every ready Gateway holds the same versioned
`deviceId -> SOCKS5` routing snapshot.

## What is implemented

- Go control plane with device, credential, route, status, interrupt, and gateway APIs.
- CAS route updates with `PREPARE -> database ACTIVATE -> ACTIVATE` acknowledgements from every connected Gateway.
- Go Gateway with embedded GOST Shadowsocks protocol support, dynamically reconciled per-device legacy/SS2022 listeners, SOCKS5 username/password negotiation, bidirectional TCP relay, connection interruption, draining, health, readiness, and Prometheus metrics.
- Encrypted SOCKS5 passwords in PostgreSQL and encrypted last-known snapshots on Gateways.
- Data-driven multi-provider adapters with encrypted supplier secrets, location/session templates, country port ranges, and weighted selection.
- Local Docker Compose, Alibaba Cloud ROS templates, cloud-init lifecycle scripts, and GitHub Actions CI/release/scale workflows.

The first release uses an NLB TCP port-range listener. NLB preserves the frontend
port when forwarding to Gateway, so port `50000` remains port `50000`; Shadowsocks
encryption terminates only inside Gateway. UDP is intentionally unsupported.

## Local start

```bash
./scripts/local-up.sh
```

This builds the two scratch-based application images, starts PostgreSQL, applies
all embedded database migrations, waits for Gateway snapshot synchronization, and fails
unless at least one Gateway reports `READY`. The default local admin token is
`local-admin-token`. Copy `.env.example` to `.env` and replace every secret before
using a shared development machine; when changing the token, export the same
`ADMIN_TOKEN` while running the script.

Create a device while retaining an existing sing-box port, method and password:

```bash
curl -sS http://127.0.0.1:8080/v1/devices \
  -H 'Authorization: Bearer local-admin-token' \
  -H 'Content-Type: application/json' \
  -d '{"id":"device-001","listenPort":50000,"shadowsocksMethod":"aes-256-gcm","shadowsocksPassword":"existing-device-password"}'
```

Omit `listenPort` and `shadowsocksPassword` for a new device; Control Plane
allocates a port and returns the generated password once.

Create an SS2022 device by setting `shadowsocksMethod` to
`2022-blake3-aes-128-gcm`. When its password is omitted, Control Plane generates
the required Base64-encoded 16-byte PSK. Existing legacy devices are never
silently upgraded.

For an online migration, add an SS2022 ingress with
`POST /v1/devices/{deviceId}/ingresses`. Both ports identify the same device and
share one dynamically updated SOCKS5 route. After changing sing-box and
verifying traffic, retire the legacy port with
`DELETE /v1/devices/{deviceId}/ingresses/{port}`.

Assign its first route, using `expectedVersion: 0`:

```bash
curl -sS -X PUT http://127.0.0.1:8080/v1/devices/device-001/route \
  -H 'Authorization: Bearer local-admin-token' \
  -H 'Content-Type: application/json' \
  -d '{"host":"socks.example.net","port":1080,"username":"user","password":"secret","expectedVersion":0}'
```

The route request succeeds only after all currently connected Gateways acknowledge both phases. New connections use the new route; existing connections remain until they close or the interrupt API is called.

## Production configuration

Control Plane requires `DATABASE_URL`, `ENCRYPTION_KEY`, and `ADMIN_TOKEN`. Gateway requires `CONTROL_GRPC_ADDR`, `SNAPSHOT_KEY` (or `ENCRYPTION_KEY`), and a writable `SNAPSHOT_PATH`. Production should additionally configure mutual TLS through the `GRPC_TLS_*` and `CONTROL_TLS_*` variables and set `REQUIRE_CANARY=true` with `CANARY_DEVICE_ID`.

Phase one injects secrets through ROS `NoEcho` parameters and stores bootstrap
material only in root-owned `0600` files on ECS. Base64 transport is not
encryption: limit ROS/ECS administrator access, never commit generated parameter
files, and migrate to a dedicated secret manager when the operator boundary
expands. See the [deployment guide](docs/deployment.md), [Alibaba staging
checklist](docs/alibaba-staging-checklist.md), and [operations
runbook](docs/runbook.md).

## Current Alibaba staging handoff

The validated staging stack is `proxymesh-staging` in `cn-hongkong`, Resource
Group `wucha_edm-sqd` (`rg-aeky5chnwj55sta`). It runs one private Control Plane
ECS and two healthy Gateway ECS instances across Hong Kong Zone B/C.

Point the device-facing DNS name at the public Gateway NLB:

```text
proxy-mesh.lintan-mob.com CNAME nlb-o1j1jeu96kcsh5gzm5.cn-hongkong.nlb.aliyuncsslbintl.com
```

The Control API NLB is intentionally private. Use a VPN, bastion, or self-hosted
runner inside the VPC for device creation and route changes. For non-mutating
cloud health checks from an operator machine with Alibaba Cloud permissions:

```bash
REGION=cn-hongkong ALIYUN_PROFILE=hz ALIYUN_BIN=/usr/local/bin/aliyun \
  scripts/aliyun-control-smoke.sh proxymesh-staging
```

Do not pass SOCKS5 passwords or device Shadowsocks passwords through Cloud
Assistant command content. Use the private Control API path for secret-bearing
operations.

## Verification

```bash
go test ./...
go vet ./...
docker compose config
REGION=cn-hongkong ALIYUN_PROFILE=hz scripts/wait-ros-stack.sh proxymesh-staging
REGION=cn-hongkong ALIYUN_PROFILE=hz scripts/aliyun-control-smoke.sh proxymesh-staging
./tests/integration/ss2022/run.sh
```

The OpenAPI contract is in `api/openapi/openapi.yaml`; the Gateway control contract is in `api/proto/control.proto`.

See `docs/providers.md` for configuring supplier-specific SOCKS5 generation rules without putting supplier credentials in source code.
