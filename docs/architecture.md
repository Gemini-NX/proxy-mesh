# Architecture

```text
sing-box -- Shadowsocks legacy or 2022 --> public NLB TCP port range
                                                  |
                                                  +--> Gateway same device port (any AZ)
                                                           |
                                                           +-- SOCKS5/TCP --> third-party proxy

Control API --> PostgreSQL
      |
      +-- bidirectional gRPC --> every Gateway
```

The NLB listens on the allocated device port range and forwards every connection
to the same port on a healthy Gateway. The port selects the device's independent
Shadowsocks listener; its password authenticates and decrypts the connection.
Therefore failover does not change device configuration. Each Gateway reads that
device's SOCKS5 route from an atomically updated in-memory table, so route lookup
does not call PostgreSQL or the control plane on the connection path. Changing a
SOCKS5 route does not close or recreate the Shadowsocks listener.

Gateway embeds `go-gost/go-shadowsocks2` rather than running a GOST sidecar.
This keeps protocol termination and the device's atomically updated SOCKS5 route
in one process. Legacy `aes-256-gcm` and `2022-blake3-aes-128-gcm` listeners can
run simultaneously on different device ports, enabling device-by-device
migration without changing other devices or reloading upstream routes.

Route publication is fail-closed: stage with CAS, PREPARE every connected Gateway, activate the database record, then ACTIVATE every Gateway. A failed PREPARE leaves the prior active route in use. An unhealthy node should first be marked DRAINING, which makes `/ready` return 503, and publication can then be retried after it disconnects from the active set.

Gateway keeps its last encrypted snapshot when control services are unavailable. A fresh node remains unready until it receives a full snapshot. A restarted node may load the local snapshot for recovery but stays unready until the control stream has reconciled it.
