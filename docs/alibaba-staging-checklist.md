# Alibaba Cloud staging checklist

The first stack creation is intentionally a user-authorized operation because it
creates billable Alibaba Cloud resources. Application releases and later
capacity changes are automated by GitHub Actions after this bootstrap.

## Values the operator must provide

- An existing `cn-hongkong` VPC ID, its CIDR, and two vSwitch IDs in distinct
  zones that support the chosen ECS and RDS types.
- A current Ubuntu 24.04 LTS x86_64 or Alibaba Cloud Linux 3 image ID and an
  available ECS instance type. Bootstrap supports both apt and yum/dnf systems.
- Private GHCR packages `ghcr.io/gemini-nx/proxy-mesh-gateway` and
  `ghcr.io/gemini-nx/proxy-mesh-control-plane`.
- A dedicated GitHub PAT classic with only the access required to read those
  packages. Export its owner as `GHCR_USERNAME` and token as `GHCR_TOKEN`; the
  parameter builder Base64-encodes them into a ROS `NoEcho` parameter.
- The device source CIDR. When mainland carrier NAT addresses change frequently,
  use `0.0.0.0/0`; access then relies on per-device Shadowsocks credentials and
  unknown ports remain closed on every Gateway.
- A PostgreSQL master password, a 32-byte base64 encryption key, and a random
  admin token. Do not commit any of them.
- A private CA, a Control Plane server certificate, and a Gateway client
  certificate. The server certificate must cover the internal control name used
  by `controlTLSServerName`; the client certificate must permit client auth.
- A DNS name such as `proxy.example.com`. After stack creation, point it at the
  public NLB `NLBDNSName` output.

`RUNTIME_SECRET_DATA` must contain:

```text
encryptionKey, adminToken, publicProxyHost, canaryDeviceId,
controlTLSServerName, controlServerCA, controlServerCert, controlServerKey,
gatewayClientCA, gatewayClientCert, gatewayClientKey
```

For staging, generate correctly scoped material with:

```bash
scripts/generate-runtime-secrets.sh /private/tmp/proxymesh-staging-secrets \
  control.internal proxy.example.com canary-001
export RUNTIME_SECRET_DATA="$(cat /private/tmp/proxymesh-staging-secrets/runtime-secret.json)"
export DB_PASSWORD="$(cat /private/tmp/proxymesh-staging-secrets/database-password.txt)"
export GHCR_USERNAME="your-github-token-owner"
read -r -s GHCR_TOKEN && export GHCR_TOKEN
```

Keep `ca-key.pem` offline and do not reuse staging certificates or tokens in
production.

The initial Hong Kong staging environment uses VPC `vpc-j6cpr1r4ypbtwsq176yc6`
with CIDR `172.16.0.0/16`, Zone B vSwitch
`vsw-j6cbdz8nh1gawb7rxijz5`, and Zone C vSwitch
`vsw-j6cnpdya6z622y4vnemmr`.

## Account preparation

1. Enable ROS, ECS, ESS, NLB, NAT Gateway/EIP, RDS PostgreSQL, OOS,
   Cloud Assistant, SLS and CloudMonitor in `cn-hongkong`.
2. Confirm quota for one public and one private NLB, two initial Gateway ECS instances,
   one Control Plane ECS instance, an ESS maximum of 20, one NAT Gateway/EIP,
   and one multi-zone RDS instance. The public dual-zone NLB consumes two EIPs
   and the NAT Gateway consumes one, so the EIP quota must be at least the
   account's current usage plus three (`eip` quota `q_6arozx`).
3. Create a PAT classic with `read:packages`, authorize it for the organization
   if SSO is enforced, and keep the two GHCR packages private.
4. Configure GitHub OIDC trust and the release RAM role, restricted to this
   repository and the matching GitHub Environment.

## First stack creation

Export the required values listed by `scripts/build-ros-parameters.sh`, then run:

```bash
scripts/build-ros-parameters.sh /private/tmp/proxymesh-staging-parameters.json
aliyun ros ValidateTemplate --RegionId cn-hongkong --TemplateBody "$(cat infra/ros/main.yaml)"
```

Create a ROS change set of type `CREATE` using `infra/ros/main.yaml`,
`infra/ros/stack-policy.json`, and the generated parameter file. Inspect it in
the ROS console, then execute it. The stack policy prevents normal updates from
deleting or replacing either NLB or PostgreSQL. Keep
`RequireCanary=false` for this first creation: otherwise the first Gateways
cannot become ready before the canary device exists.

For Alibaba Cloud CLI 3.0.x, use the compatibility wrapper so repeated ROS
parameters are encoded correctly without printing secret values:

```bash
ALIYUN_PROFILE=hz scripts/create-ros-change-set.sh \
  proxymesh-staging initial-staging-YYYYMMDD \
  /private/tmp/proxymesh-staging-parameters.json
```

After the stack reaches `CREATE_COMPLETE`:

1. Add `proxy.example.com` as a CNAME to `NLBDNSName`.
2. Verify the private Control Plane and the Gateway registration set without
   sending secrets through Cloud Assistant command content:

   ```bash
   REGION=cn-hongkong ALIYUN_PROFILE=hz ALIYUN_BIN=/usr/local/bin/aliyun \
     scripts/aliyun-control-smoke.sh proxymesh-staging
   ```

3. Reach the private `ControlAPIDNSName:8080` through a VPN, bastion, or a
   self-hosted GitHub runner in the VPC for all secret-bearing API calls.
   Route updates include upstream SOCKS5 passwords; do not encode those request
   bodies into ECS RunCommand/Cloud Assistant commands because command content
   is retained in cloud-side execution records.
4. Create the canary device and assign a known-good SOCKS5 route.
5. Update `RequireCanary=true` through a guarded ROS change set and perform one
   rolling Gateway replacement.
6. Run the failure and load suites before creating the production stack.

The validated staging stack created on 2026-07-18 uses Resource Group
`wucha_edm-sqd` (`rg-aeky5chnwj55sta`) and has these operator-facing outputs:

```text
Gateway public NLB: nlb-o1j1jeu96kcsh5gzm5.cn-hongkong.nlb.aliyuncsslbintl.com
Control private NLB: nlb-6evyw9iljszkc9ltfy.cn-hongkong.nlb.aliyuncsslbintl.com
NAT EIP: 47.243.127.214
PostgreSQL endpoint: pgm-j6c1800gkavvka3m.pg.cnhk.rds.aliyuncs.com
Gateway ESS group: asg-j6c6h0zf9t0bmw6kblxo
```

Set `proxy-mesh.lintan-mob.com` as a CNAME to the Gateway public NLB before
handing sing-box snippets to devices.

Initial staging uses `GatewayDesiredCapacity=2` and
`EnableAutoScaleOut=false`. Before migrating real device volume, raise the
desired capacity to at least 4 and explicitly enable automatic scale-out.

Delete the generated parameter JSON after the stack is created. It contains
bootstrap secrets even though its file mode is `0600` and ROS marks the
corresponding parameters `NoEcho`. Base64 does not encrypt those values. The
same bootstrap values remain available to the ECS root user so services can
restart and newly scaled Gateway instances can authenticate to private GHCR.

## GitHub Environment values

Set these separately for `staging` and `production`:

```text
ALIBABA_CLOUD_OIDC_PROVIDER_ARN
ALIBABA_CLOUD_RELEASE_ROLE_ARN
DEPLOY_ENABLED
ROS_STACK_NAME
GATEWAY_SCALING_GROUP_ID
```

Protect the `production` Environment with required reviewers. The release role
needs only ROS change-set/stack update, ESS describe/modify/remove,
and the read permissions needed to inspect rollout state. No long-lived
AccessKey is stored in GitHub.

Keep repository variable `DEPLOY_ENABLED=false` until the first ROS stack is
created and both GitHub Environments are configured. Main-branch pushes still
build and publish immutable GHCR images; cloud deployment jobs remain skipped.
