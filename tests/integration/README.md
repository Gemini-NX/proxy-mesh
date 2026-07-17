# Integration tests

`smoke.sh` is intentionally non-mutating and runs after deployment. It verifies that the control API is live and at least `MIN_READY_GATEWAYS` Gateways report ready (default 2 for staging). The Go test in `internal/dataplane` supplies an authenticated SOCKS5 server and echo target, then verifies a complete `aes-256-gcm` Shadowsocks tunnel and confirms that an upstream route change does not restart the device listener.

For a staging route-isolation test, provision two disposable devices and two SOCKS5 echo endpoints, update only one route with CAS, and repeat a new connection through every NLB backend. Do not reuse production proxy credentials in test fixtures.
