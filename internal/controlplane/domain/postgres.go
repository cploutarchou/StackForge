package domain

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

type PostgresStore struct {
	db *sql.DB
}

func NewPostgresStore(ctx context.Context, databaseURL string) (*PostgresStore, error) {
	if databaseURL == "" {
		return nil, errors.New("DATABASE_URL is required")
	}
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, err
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	s := &PostgresStore{db: db}
	if err := s.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *PostgresStore) Create(d Domain, allowWildcard bool) (Domain, error) {
	if err := ValidateName(d.Domain, allowWildcard); err != nil {
		return Domain{}, err
	}
	if d.TenantID == "" || d.TargetServiceName == "" || d.TargetServicePort <= 0 {
		return Domain{}, errors.New("tenant_id, target_service_name, and target_service_port are required")
	}
	now := time.Now().UTC()
	d.Domain = normalizeDomain(d.Domain)
	d.ID = tokenID()
	d.RootDomain = rootDomain(d.Domain)
	d.Subdomain = subdomain(d.Domain)
	if d.Provider == "" {
		d.Provider = "cloudflare"
	}
	if d.DNSRecordType == "" {
		d.DNSRecordType = "CNAME"
	}
	if d.Environment == "" {
		d.Environment = "production"
	}
	d.OwnershipStatus = "pending"
	d.RoutingStatus = "pending"
	d.DeploymentStatus = "pending"
	d.CreatedAt = now
	d.UpdatedAt = now
	_, err := s.db.Exec(`INSERT INTO domains (id, tenant_id, domain, root_domain, subdomain, target_service_name, target_service_port, environment, provider, provider_zone_id, provider_record_id, dns_record_type, dns_record_value, proxied, ssl_enabled, ownership_status, routing_status, deployment_status, last_error, created_at, updated_at, deleted_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22)`, d.ID, d.TenantID, d.Domain, d.RootDomain, d.Subdomain, d.TargetServiceName, d.TargetServicePort, d.Environment, d.Provider, nullString(d.ProviderZoneID), nullString(d.ProviderRecordID), d.DNSRecordType, nullString(d.DNSRecordValue), d.Proxied, d.SSLEnabled, d.OwnershipStatus, d.RoutingStatus, d.DeploymentStatus, nullString(d.LastError), d.CreatedAt, d.UpdatedAt, d.DeletedAt)
	if err != nil {
		return Domain{}, err
	}
	return d, nil
}

func (s *PostgresStore) List() []Domain {
	rows, err := s.db.Query(selectDomainSQL + ` WHERE deleted_at IS NULL ORDER BY created_at`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []Domain
	for rows.Next() {
		if d, err := scanDomain(rows); err == nil {
			out = append(out, d)
		}
	}
	return out
}

func (s *PostgresStore) Get(id string) (Domain, bool) {
	row := s.db.QueryRow(selectDomainSQL+` WHERE deleted_at IS NULL AND (id=$1 OR domain=$1)`, id)
	d, err := scanDomain(row)
	return d, err == nil
}

func (s *PostgresStore) Delete(id string) error {
	res, err := s.db.Exec(`UPDATE domains SET deleted_at=$1, routing_status='disabled', updated_at=$1 WHERE deleted_at IS NULL AND (id=$2 OR domain=$2)`, time.Now().UTC(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errors.New("not found")
	}
	return nil
}

func (s *PostgresStore) VerificationToken(id string) (Verification, error) {
	d, ok := s.Get(id)
	if !ok {
		return Verification{}, errors.New("domain not found")
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return Verification{}, err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	hash := sha256.Sum256([]byte(token))
	v := Verification{DomainID: d.ID, TXTName: "_stackforge-verify." + d.Domain, Token: token, TokenHash: hex.EncodeToString(hash[:]), CreatedAt: time.Now().UTC()}
	_, err := s.db.Exec(`INSERT INTO domain_verifications (id, domain_id, txt_name, token_hash, created_at) VALUES ($1,$2,$3,$4,$5)
ON CONFLICT (id) DO UPDATE SET txt_name=EXCLUDED.txt_name, token_hash=EXCLUDED.token_hash, created_at=EXCLUDED.created_at, verified_at=NULL`, tokenID(), v.DomainID, v.TXTName, v.TokenHash, v.CreatedAt)
	return v, err
}

func (s *PostgresStore) MarkVerified(id string, token string) error {
	d, ok := s.Get(id)
	if !ok {
		return errors.New("domain not found")
	}
	var hash string
	err := s.db.QueryRow(`SELECT token_hash FROM domain_verifications WHERE domain_id=$1 ORDER BY created_at DESC LIMIT 1`, d.ID).Scan(&hash)
	if err != nil {
		return errors.New("verification token not generated")
	}
	got := sha256.Sum256([]byte(token))
	if hex.EncodeToString(got[:]) != hash {
		return errors.New("verification token mismatch")
	}
	now := time.Now().UTC()
	_, err = s.db.Exec(`UPDATE domains SET ownership_status='verified', updated_at=$1 WHERE id=$2`, now, d.ID)
	if err != nil {
		return err
	}
	_, _ = s.db.Exec(`UPDATE domain_verifications SET verified_at=$1 WHERE domain_id=$2`, now, d.ID)
	return nil
}

func (s *PostgresStore) migrate(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, postgresSchema)
	return err
}

type scanner interface {
	Scan(dest ...any) error
}

const selectDomainSQL = `SELECT id, tenant_id, domain, root_domain, subdomain, target_service_name, target_service_port, environment, provider, provider_zone_id, provider_record_id, dns_record_type, dns_record_value, proxied, ssl_enabled, ownership_status, routing_status, deployment_status, last_error, created_at, updated_at, deleted_at FROM domains`

func scanDomain(row scanner) (Domain, error) {
	var d Domain
	var zone, record, value, last sql.NullString
	var deleted sql.NullTime
	err := row.Scan(&d.ID, &d.TenantID, &d.Domain, &d.RootDomain, &d.Subdomain, &d.TargetServiceName, &d.TargetServicePort, &d.Environment, &d.Provider, &zone, &record, &d.DNSRecordType, &value, &d.Proxied, &d.SSLEnabled, &d.OwnershipStatus, &d.RoutingStatus, &d.DeploymentStatus, &last, &d.CreatedAt, &d.UpdatedAt, &deleted)
	if err != nil {
		return Domain{}, err
	}
	d.ProviderZoneID = zone.String
	d.ProviderRecordID = record.String
	d.DNSRecordValue = value.String
	d.LastError = last.String
	if deleted.Valid {
		d.DeletedAt = &deleted.Time
	}
	return d, nil
}

func nullString(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}

func normalizeDomain(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

const postgresSchema = `
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
CREATE UNIQUE INDEX IF NOT EXISTS domains_active_domain_unique ON domains(domain) WHERE deleted_at IS NULL;
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
);`

var _ Repository = (*PostgresStore)(nil)

func (s *PostgresStore) String() string {
	return fmt.Sprintf("postgres-store(%p)", s.db)
}
