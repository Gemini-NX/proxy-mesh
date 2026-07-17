# Failover validation

Run `gateway-blackhole.sh` only in staging. `GATEWAY_HEALTH_URL` must reach one Gateway over the private network because `/drain` rejects non-loopback callers by default; in production the scale-in lifecycle script runs locally on the ECS instance. For remote chaos tests, execute the script through OOS RunCommand on the target instance.

The expected sequence is readiness 503, NLB removal after two failed five-second checks, and a new device connection reaching a different Gateway. Existing connections are allowed to drain for up to ten minutes.
