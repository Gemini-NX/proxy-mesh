# Alibaba Cloud staging checklist

The first stack creation is intentionally a user-authorized operation because it
creates billable Alibaba Cloud resources. Application releases and later
capacity changes are automated by GitHub Actions after this bootstrap.

## Values the operator must provide

- Two distinct `cn-hongkong` zone IDs that support the chosen ECS and RDS types.
- A current Alibaba Cloud Linux 3 image ID and an available ECS instance type.
- An ACR registry/namespace containing the Gateway and Control Plane image digests.
- The office public CIDR. Use a narrow `/32` or corporate NAT range; avoid the
  example default `0.0.0.0/0` in production.
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
```

Keep `ca-key.pem` offline and do not reuse staging certificates or tokens in
production.

## Account preparation

1. Enable ROS, ECS, ESS, NLB, NAT Gateway/EIP, RDS PostgreSQL, KMS, OOS,
   Cloud Assistant, ACR, SLS and CloudMonitor in `cn-hongkong`.
2. Confirm quota for one public and one private NLB, four Gateway ECS instances,
   one Control Plane ECS instance, an ESS maximum of 20, one NAT Gateway/EIP,
   and one multi-zone RDS instance.
3. Create the ACR namespace and repositories `proxymesh/gateway` and
   `proxymesh/control-plane`.
4. Configure GitHub OIDC trust and the release RAM role, restricted to this
   repository and the matching GitHub Environment.

## First stack creation

Export the required values listed by `scripts/build-ros-parameters.sh`, then run:

```bash
scripts/build-ros-parameters.sh /private/tmp/proxymesh-staging-parameters.json
aliyun ros ValidateTemplate --RegionId cn-hongkong --TemplateBody "$(cat infra/ros/main.yaml)"
```

Create a ROS change set of type `CREATE` using `infra/ros/main.yaml` and the
generated parameter file, inspect it in the ROS console, then execute it. Keep
`RequireCanary=false` for this first creation: otherwise the first Gateways
cannot become ready before the canary device exists.

After the stack reaches `CREATE_COMPLETE`:

1. Add `proxy.example.com` as a CNAME to `NLBDNSName`.
2. Reach the private `ControlAPIDNSName:8080` through a VPN, bastion, or a
   self-hosted GitHub runner in the VPC.
3. Create the canary device and assign a known-good SOCKS5 route.
4. Update `RequireCanary=true` through a guarded ROS change set and perform one
   rolling Gateway replacement.
5. Run the failure and load suites before creating the production stack.

Delete the generated parameter JSON after the stack is created. It contains
bootstrap secrets even though its file mode is `0600`.

## GitHub Environment values

Set these separately for `staging` and `production`:

```text
ALIBABA_CLOUD_OIDC_PROVIDER_ARN
ALIBABA_CLOUD_RELEASE_ROLE_ARN
ACR_REGISTRY
ROS_STACK_NAME
GATEWAY_SCALING_GROUP_ID
```

Protect the `production` Environment with required reviewers. The release role
needs only ACR push, ROS change-set/stack update, ESS describe/modify/remove,
and the read permissions needed to inspect rollout state. No long-lived
AccessKey is stored in GitHub.
