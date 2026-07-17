ALTER TABLE devices
  ADD COLUMN IF NOT EXISTS ingress_port integer,
  ADD COLUMN IF NOT EXISTS ingress_method text NOT NULL DEFAULT 'aes-256-gcm',
  ADD COLUMN IF NOT EXISTS ingress_password_cipher bytea;

CREATE UNIQUE INDEX IF NOT EXISTS devices_ingress_port_unique
  ON devices(ingress_port) WHERE ingress_port IS NOT NULL;

ALTER TABLE devices
  DROP CONSTRAINT IF EXISTS devices_ingress_port_check;

ALTER TABLE devices
  ADD CONSTRAINT devices_ingress_port_check
  CHECK (ingress_port IS NULL OR ingress_port BETWEEN 1 AND 65535);
