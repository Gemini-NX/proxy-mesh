CREATE TABLE IF NOT EXISTS proxy_providers (
  id text PRIMARY KEY,
  enabled boolean NOT NULL DEFAULT true,
  weight integer NOT NULL DEFAULT 0 CHECK (weight BETWEEN 0 AND 10000),
  config jsonb NOT NULL,
  secrets_cipher bytea NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE proxy_credentials
  ADD COLUMN IF NOT EXISTS provider_id text REFERENCES proxy_providers(id),
  ADD COLUMN IF NOT EXISTS generation_metadata jsonb NOT NULL DEFAULT '{}'::jsonb;
