# Alibaba Cloud deployment

1. Build both images and push immutable Git-SHA tags to ACR.
2. Reserve a device ingress range (default `50000..59999`). NLB uses TCP
   multi-port listening and forwards each frontend port to the identical backend
   port; it does not terminate Shadowsocks encryption.
3. Create a KMS secret value containing runtime configuration. It must contain
   `encryptionKey`, `canaryDeviceId`, `controlTLSServerName`, `controlCA`,
   `controlCert`, and `controlKey`. The cloud-init script writes the three PEM
   values to a root-only runtime directory, transfers ownership to the non-root
   Gateway UID, and mounts that directory read-only into the container. Use a
   ROS NoEcho parameter only for initial bootstrap; rotate it in KMS afterward.
4. Create a ROS change set from `infra/ros/main.yaml`. Review it and reject any change that replaces or deletes the NLB, RDS, or KMS secret.
5. Execute the change set. ESS creates four Gateways across two vSwitches. Scale-out remains pending until the lifecycle script observes `/ready=200`.
6. Create a DNS CNAME from the public proxy hostname to the ROS `NLBDNSName` output.

The template leaves application image, port range, image ID, instance type,
database password, and KMS bootstrap data as parameters. The production release
workflow uses GitHub OIDC and a RAM role; no AccessKey is stored in GitHub. A
production rollout temporarily adds one ready instance, drains and removes one
old instance, and repeats. This preserves the ESS `BALANCE` multi-zone policy and
never rolls more than one old Gateway at a time.

For fast capacity changes, dispatch `scale.yml` with the desired value. It creates and reviews a ROS change set that changes only `GatewayDesiredCapacity` within 4..20.

## GitHub environment configuration

Create Alibaba RAM OIDC trust for `https://token.actions.githubusercontent.com`, restrict the audience and repository/environment subject, then grant the release role only ACR push, ROS change-set/stack update, and the required ESS read/modify/remove actions. Configure `ALIBABA_CLOUD_OIDC_PROVIDER_ARN`, `ALIBABA_CLOUD_RELEASE_ROLE_ARN`, `ACR_REGISTRY`, `ROS_STACK_NAME`, `CONTROL_GRPC_ADDR`, and `GATEWAY_SCALING_GROUP_ID` as environment variables. Keep the smoke admin token as an environment secret. Configure required reviewers on the `production` GitHub Environment.

The ROS stack accepts a private `ControlGRPCEndpoint`; run at least two control-plane replicas behind an internal endpoint. Give those replicas RDS connectivity and runtime access to the same KMS-managed encryption key, and configure gRPC mutual TLS. The phase-one template intentionally does not expose the control API or gRPC endpoint publicly.
