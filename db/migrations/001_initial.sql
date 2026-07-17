CREATE TABLE IF NOT EXISTS devices (
  id text PRIMARY KEY,
  username text NOT NULL UNIQUE,
  password_hash text NOT NULL,
  enabled boolean NOT NULL DEFAULT true,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS proxy_credentials (
  id text PRIMARY KEY,
  host text NOT NULL,
  port integer NOT NULL CHECK (port BETWEEN 1 AND 65535),
  username text NOT NULL DEFAULT '',
  password_cipher bytea NOT NULL,
  expires_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS device_routes (
  device_id text NOT NULL REFERENCES devices(id),
  credential_id text NOT NULL REFERENCES proxy_credentials(id),
  version bigint NOT NULL,
  status text NOT NULL CHECK (status IN ('pending', 'active', 'superseded')),
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (device_id, version)
);
CREATE UNIQUE INDEX IF NOT EXISTS one_active_route_per_device
  ON device_routes(device_id) WHERE status = 'active';

CREATE TABLE IF NOT EXISTS gateways (
  id text PRIMARY KEY,
  address text NOT NULL DEFAULT '',
  status text NOT NULL,
  applied_version bigint NOT NULL DEFAULT 0,
  active_connections bigint NOT NULL DEFAULT 0,
  last_heartbeat_at timestamptz NOT NULL
);

CREATE TABLE IF NOT EXISTS route_deployments (
  device_id text NOT NULL,
  route_version bigint NOT NULL,
  gateway_id text NOT NULL,
  phase text NOT NULL,
  success boolean NOT NULL,
  error text NOT NULL DEFAULT '',
  acknowledged_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY(device_id, route_version, gateway_id, phase)
);

CREATE TABLE IF NOT EXISTS audit_events (
  id bigserial PRIMARY KEY,
  actor text NOT NULL,
  action text NOT NULL,
  resource text NOT NULL,
  details jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_at timestamptz NOT NULL DEFAULT now()
);
