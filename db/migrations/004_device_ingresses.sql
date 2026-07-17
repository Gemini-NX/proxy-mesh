CREATE TABLE IF NOT EXISTS device_ingresses (
  device_id text NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
  port integer NOT NULL CHECK (port BETWEEN 1 AND 65535),
  method text NOT NULL,
  password_cipher bytea NOT NULL,
  is_primary boolean NOT NULL DEFAULT false,
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (device_id, port),
  UNIQUE (port)
);

CREATE UNIQUE INDEX IF NOT EXISTS one_primary_ingress_per_device
  ON device_ingresses(device_id) WHERE is_primary;

INSERT INTO device_ingresses(device_id, port, method, password_cipher, is_primary, created_at)
SELECT id, ingress_port, ingress_method, ingress_password_cipher, true, created_at
FROM devices
WHERE ingress_port IS NOT NULL AND ingress_password_cipher IS NOT NULL
ON CONFLICT (device_id, port) DO NOTHING;
