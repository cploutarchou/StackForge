CREATE TABLE IF NOT EXISTS domains (
  id TEXT PRIMARY KEY,
  tenant_id TEXT NOT NULL,
  domain TEXT NOT NULL,
  root_domain TEXT NOT NULL,
  subdomain TEXT NOT NULL DEFAULT '',
  target_service_name TEXT NOT NULL,
  target_service_port INTEGER NOT NULL,
  environment TEXT NOT NULL,
  provider TEXT NOT NULL DEFAULT 'cloudflare',
  provider_zone_id TEXT,
  provider_record_id TEXT,
  dns_record_type TEXT NOT NULL DEFAULT 'CNAME',
  dns_record_value TEXT,
  proxied BOOLEAN NOT NULL DEFAULT false,
  ssl_enabled BOOLEAN NOT NULL DEFAULT true,
  ownership_status TEXT NOT NULL DEFAULT 'pending',
  routing_status TEXT NOT NULL DEFAULT 'pending',
  deployment_status TEXT NOT NULL DEFAULT 'pending',
  last_error TEXT,
  created_at TIMESTAMP NOT NULL,
  updated_at TIMESTAMP NOT NULL,
  deleted_at TIMESTAMP NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS domains_active_domain_unique
ON domains(domain)
WHERE deleted_at IS NULL;

CREATE TABLE IF NOT EXISTS domain_verifications (
  id TEXT PRIMARY KEY,
  domain_id TEXT NOT NULL REFERENCES domains(id),
  txt_name TEXT NOT NULL,
  token_hash TEXT NOT NULL,
  created_at TIMESTAMP NOT NULL,
  verified_at TIMESTAMP NULL
);

CREATE TABLE IF NOT EXISTS audit_logs (
  id TEXT PRIMARY KEY,
  actor TEXT NOT NULL,
  action TEXT NOT NULL,
  resource_type TEXT NOT NULL,
  resource_id TEXT NOT NULL,
  before_json TEXT,
  after_json TEXT,
  error TEXT,
  created_at TIMESTAMP NOT NULL
);

CREATE TABLE IF NOT EXISTS reconcile_locks (
  domain_id TEXT PRIMARY KEY,
  holder TEXT NOT NULL,
  expires_at TIMESTAMP NOT NULL,
  created_at TIMESTAMP NOT NULL
);
