# ProxyMesh staging handoff

Last verified from this workspace: `proxymesh-staging` in `cn-hongkong`.

## Current staging resources

```text
Resource Group: wucha_edm-sqd / rg-aeky5chnwj55sta
ROS Stack: proxymesh-staging
Gateway ASG: asg-j6c6h0zf9t0bmw6kblxo
Gateway NLB: nlb-o1j1jeu96kcsh5gzm5.cn-hongkong.nlb.aliyuncsslbintl.com
Control NLB: private only
NAT EIP: 47.243.127.214
Gateway desired capacity: 2
Gateway max capacity: 20
```

The Control API is intentionally private. Secret-bearing operations such as
device creation and SOCKS5 route updates must run through VPN, bastion, or a
self-hosted runner inside the VPC.

## Required external action

Add this DNS record in the authoritative DNS provider for `lintan-mob.com`:

```text
proxy-mesh.lintan-mob.com CNAME nlb-o1j1jeu96kcsh5gzm5.cn-hongkong.nlb.aliyuncsslbintl.com
```

Until this record exists, devices can still be tested against the raw NLB DNS
name, but the production sing-box snippets should use `proxy-mesh.lintan-mob.com`.

## Acceptance command

From this repository:

```bash
REGION=cn-hongkong \
ALIYUN_PROFILE=hz \
ALIYUN_BIN=/usr/local/bin/aliyun \
GO_BIN=/opt/homebrew/bin/go \
DOCKER_BIN=/usr/local/bin/docker \
scripts/verify-delivery.sh proxymesh-staging
```

The script runs local tests, ROS template validation, private control smoke, and
a real public data-plane canary:

```text
sing-box client -> public NLB -> Gateway Shadowsocks -> SOCKS5 route -> example.com
```

The DNS step is advisory because DNS is outside the current Alibaba Cloud
account.

## First real-device grey release

1. Add the DNS CNAME above.
2. Connect to the private Control API from inside the VPC.
3. Import one existing device sing-box Shadowsocks outbound:

   ```bash
   DEVICE_ID=device-001 \
   CONTROL_URL=http://<private-control-host>:8080 \
   ADMIN_TOKEN=... \
   scripts/import-singbox-device.sh /path/to/sing-box.json
   ```

4. Store the upstream SOCKS5 route request in a local `0600` file, then publish
   it through the private Control API:

   ```bash
   DEVICE_ID=device-001 \
   CONTROL_URL=http://<private-control-host>:8080 \
   ADMIN_TOKEN=... \
   scripts/put-device-route.sh 0 /private/tmp/device-001-route.json
   ```

5. Restart or reload sing-box on that one device and test new TCP traffic.
6. If successful, migrate more devices in small batches.

Keep upstream SOCKS5 passwords and device Shadowsocks passwords out of
RunCommand/OOS metadata/GitHub logs.
