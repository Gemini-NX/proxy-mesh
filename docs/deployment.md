# Alibaba Cloud deployment

1. Build both images and push immutable Git-SHA tags and digests to private GHCR
   packages under `ghcr.io/gemini-nx`.
2. Reserve a device ingress range (default `50000..59999`). NLB uses TCP
   multi-port listening and forwards each frontend port to the identical backend
   port; it does not terminate Shadowsocks encryption.
3. Create the runtime configuration described in
   `docs/alibaba-staging-checklist.md`. Phase one transports runtime, database,
   TLS, and private GHCR credentials as Base64-encoded ROS `NoEcho` parameters.
   Cloud-init stores bootstrap data in root-owned `0600` files, writes runtime
   PEM material only under `/run/proxymesh`, and logs out of GHCR immediately
   after pulling each image. Base64 is transport encoding, not encryption.
4. Create a ROS change set from `infra/ros/main.yaml`. Review it and reject any change that replaces or deletes the NLB or RDS.
5. Execute the change set. ROS creates one private Control Plane ECS behind an
   internal NLB and ESS creates two staging Gateways across two vSwitches. Automatic scale-out is disabled for initial validation. Scale-out
   remains pending until the lifecycle script observes `/ready=200`.
6. Create a DNS CNAME from the public proxy hostname to the ROS `NLBDNSName` output.

The template leaves environment name, application images, port range, source
CIDR, image ID, instance type, and `NoEcho` bootstrap data as
parameters. The production release
workflow uses GitHub OIDC and a RAM role; no AccessKey is stored in GitHub. A
production rollout temporarily adds one ready instance, drains and removes one
old instance, and repeats. This preserves the ESS `BALANCE` multi-zone policy and
never rolls more than one old Gateway at a time.

For fast capacity changes, dispatch `scale.yml` with the desired value. It creates and reviews a ROS change set that changes only `GatewayDesiredCapacity`. Staging accepts 2..20; production retains a 4..20 safety floor.

Before production migration, raise `GatewayDesiredCapacity` to at least 4 and set
`EnableAutoScaleOut=true` through a reviewed ROS change set.

## GitHub environment configuration

Create Alibaba RAM OIDC trust for `https://token.actions.githubusercontent.com`, restrict the audience and repository/environment subject, then grant the release role only ROS change-set/stack update and the required ESS read/modify/remove actions. Configure `ALIBABA_CLOUD_OIDC_PROVIDER_ARN`, `ALIBABA_CLOUD_RELEASE_ROLE_ARN`, `ROS_STACK_NAME`, and `GATEWAY_SCALING_GROUP_ID` as environment variables. Configure required reviewers on the `production` GitHub Environment. GitHub Actions publishes GHCR images with the repository-scoped `GITHUB_TOKEN`; Alibaba credentials are not used by the image build job.

The phase-one Control Plane is intentionally a single active ECS instance because
Gateway sessions and two-phase ACK state are held in memory. Gateways continue
using encrypted local snapshots while it restarts. Horizontal Control Plane HA
requires shared session/deployment coordination and is deferred; running two
independent replicas would violate all-Gateway route activation semantics. The
control API and gRPC NLB are private and gRPC uses mutual TLS.

For the initial boot, leave `RequireCanary=false`, create the canary device and
route, then enable the flag and roll the Gateways. See
`docs/alibaba-staging-checklist.md` for the exact operator checklist.
