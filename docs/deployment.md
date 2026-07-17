# Alibaba Cloud deployment

1. Build both images and push immutable Git-SHA tags to ACR.
2. Reserve a device ingress range (default `50000..59999`). NLB uses TCP
   multi-port listening and forwards each frontend port to the identical backend
   port; it does not terminate Shadowsocks encryption.
3. Create the runtime configuration described in
   `docs/alibaba-staging-checklist.md`. ROS creates environment-scoped KMS
   secrets for runtime and database credentials. Cloud-init writes PEM material
   only to a root-owned runtime directory and mounts it read-only into the
   containers. Use ROS `NoEcho` parameters only for bootstrap and rotate the KMS
   values afterward.
4. Create a ROS change set from `infra/ros/main.yaml`. Review it and reject any change that replaces or deletes the NLB, RDS, or KMS secret.
5. Execute the change set. ROS creates one private Control Plane ECS behind an
   internal NLB and ESS creates four Gateways across two vSwitches. Scale-out
   remains pending until the lifecycle script observes `/ready=200`.
6. Create a DNS CNAME from the public proxy hostname to the ROS `NLBDNSName` output.

The template leaves environment name, application images, port range, source
CIDR, image ID, instance type, database password, and KMS bootstrap data as
parameters. The production release
workflow uses GitHub OIDC and a RAM role; no AccessKey is stored in GitHub. A
production rollout temporarily adds one ready instance, drains and removes one
old instance, and repeats. This preserves the ESS `BALANCE` multi-zone policy and
never rolls more than one old Gateway at a time.

For fast capacity changes, dispatch `scale.yml` with the desired value. It creates and reviews a ROS change set that changes only `GatewayDesiredCapacity` within 4..20.

## GitHub environment configuration

Create Alibaba RAM OIDC trust for `https://token.actions.githubusercontent.com`, restrict the audience and repository/environment subject, then grant the release role only ACR push, ROS change-set/stack update, and the required ESS read/modify/remove actions. Configure `ALIBABA_CLOUD_OIDC_PROVIDER_ARN`, `ALIBABA_CLOUD_RELEASE_ROLE_ARN`, `ACR_REGISTRY`, `ROS_STACK_NAME`, and `GATEWAY_SCALING_GROUP_ID` as environment variables. Configure required reviewers on the `production` GitHub Environment.

The phase-one Control Plane is intentionally a single active ECS instance because
Gateway sessions and two-phase ACK state are held in memory. Gateways continue
using encrypted local snapshots while it restarts. Horizontal Control Plane HA
requires shared session/deployment coordination and is deferred; running two
independent replicas would violate all-Gateway route activation semantics. The
control API and gRPC NLB are private and gRPC uses mutual TLS.

For the initial boot, leave `RequireCanary=false`, create the canary device and
route, then enable the flag and roll the Gateways. See
`docs/alibaba-staging-checklist.md` for the exact operator checklist.
