# GOST / Shadowsocks 2022 compatibility PoC

This directory records the isolated compatibility check performed before changing
the ProxyMesh gateway implementation.

## Versions tested

- sing-box `1.13.14`
- GOST stable `3.2.6`
- GOST nightly image `gogost/gost@sha256:5f0b792e12c5640b4f0afddeb8df23bdbd72c70cd67de32d983c846d33996260`
  (the binary reports GOST `3.3.0`)
- `github.com/go-gost/go-shadowsocks2 v0.1.3`

All passwords in this directory are disposable test values.

## Results

| Case | Result |
| --- | --- |
| GOST 3.2.6 + sing-box + `2022-blake3-aes-128-gcm` | Failed: request reached the target, but sing-box rejected the response with `cipher: message authentication failed` |
| GOST 3.3.0 nightly + sing-box + `2022-blake3-aes-128-gcm` | Passed: bidirectional HTTP request completed |
| One GOST process serving legacy `aes-256-gcm` and SS2022 on different ports | Passed for both ports |
| SS2022 client using the wrong key | Passed: connection was rejected and no HTTP response was returned |
| Direct compilation against `go-gost/go-shadowsocks2 v0.1.3` | Passed for legacy and SS2022 server configuration construction |

## Decision

Do not use GOST 3.2.6 for SS2022. The tested 3.3.0 nightly proves protocol
interoperability, but a prerelease GOST executable is not the preferred production
dependency. ProxyMesh therefore pins `go-gost/go-shadowsocks2 v0.1.3` and embeds
its `core.TCPServer` in Gateway. The real sing-box -> Gateway -> authenticated
SOCKS5 integration test is maintained in `tests/integration/ss2022`.

The implemented migration keeps legacy and SS2022 listeners on different ports
of the same device record. Both listeners resolve the same atomically managed
upstream SOCKS5 route, so devices can be moved one at a time without duplicating
route updates.
