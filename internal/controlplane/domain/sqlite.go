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
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type SQLiteStore struct {
	db *sql.DB
}

func NewSQLiteStore(ctx context.Context, databaseURL string) (*SQLiteStore, error) {
	if strings.TrimSpace(databaseURL) == "" {
		return nil, errors.New("DATABASE_URL is required")
	}
	dsn, dbPath, err := sqliteDSN(databaseURL)
	if err != nil {
		return nil, err
	}
	if dbPath != "" {
		if err := os.MkdirAll(filepath.Dir(dbPath), 0700); err != nil {
			return nil, err
		}
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	s := &SQLiteStore{db: db}
	if err := s.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func sqliteDSN(databaseURL string) (dsn string, dbPath string, err error) {
	v := strings.TrimSpace(databaseURL)
	withPragmas := func(base string) string {
		sep := "?"
		if strings.Contains(base, "?") {
			sep = "&"
		}
		return base + sep + "_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)"
	}
	if strings.HasPrefix(v, "sqlite://") {
		u, parseErr := url.Parse(v)
		if parseErr != nil {
			return "", "", parseErr
		}
		path := u.Path
		if path == "" {
			path = u.Host
		}
		if path == "" {
			return "", "", errors.New("sqlite DATABASE_URL path is empty")
		}
		dsn = withPragmas("file:" + path)
		unescaped, _ := url.PathUnescape(path)
		return dsn, unescaped, nil
	}
	if strings.HasPrefix(v, "file:") {
		dsn = withPragmas(v)
		path := strings.TrimPrefix(v, "file:")
		path = strings.SplitN(path, "?", 2)[0]
		unescaped, _ := url.PathUnescape(path)
		return dsn, unescaped, nil
	}
	if strings.HasSuffix(v, ".db") {
		dsn = withPragmas("file:" + v)
		return dsn, v, nil
	}
	return "", "", fmt.Errorf("unsupported sqlite DATABASE_URL: %s", databaseURL)
}

func (s *SQLiteStore) Create(d Domain, allowWildcard bool) (Domain, error) {
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
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,NULL)`, d.ID, d.TenantID, d.Domain, d.RootDomain, d.Subdomain, d.TargetServiceName, d.TargetServicePort, d.Environment, d.Provider, emptyToNil(d.ProviderZoneID), emptyToNil(d.ProviderRecordID), d.DNSRecordType, emptyToNil(d.DNSRecordValue), d.Proxied, d.SSLEnabled, d.OwnershipStatus, d.RoutingStatus, d.DeploymentStatus, emptyToNil(d.LastError), now.Unix(), now.Unix())
	if err != nil {
		return Domain{}, err
	}
	return d, nil
}

func (s *SQLiteStore) List() []Domain {
	rows, err := s.db.Query(`SELECT id, tenant_id, domain, root_domain, subdomain, target_service_name, target_service_port, environment, provider, provider_zone_id, provider_record_id, dns_record_type, dns_record_value, proxied, ssl_enabled, ownership_status, routing_status, deployment_status, last_error, created_at, updated_at, deleted_at FROM domains WHERE deleted_at IS NULL ORDER BY created_at`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []Domain
	for rows.Next() {
		d, scanErr := scanSQLiteDomain(rows)
		if scanErr == nil {
			out = append(out, d)
		}
	}
	return out
}

func (s *SQLiteStore) Get(id string) (Domain, bool) {
	row := s.db.QueryRow(`SELECT id, tenant_id, domain, root_domain, subdomain, target_service_name, target_service_port, environment, provider, provider_zone_id, provider_record_id, dns_record_type, dns_record_value, proxied, ssl_enabled, ownership_status, routing_status, deployment_status, last_error, created_at, updated_at, deleted_at FROM domains WHERE deleted_at IS NULL AND (id=? OR domain=?)`, id, id)
	d, err := scanSQLiteDomain(row)
	return d, err == nil
}

func (s *SQLiteStore) Delete(id string) error {
	now := time.Now().UTC().Unix()
	res, err := s.db.Exec(`UPDATE domains SET deleted_at=?, routing_status='disabled', updated_at=? WHERE deleted_at IS NULL AND (id=? OR domain=?)`, now, now, id, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errors.New("not found")
	}
	return nil
}

func (s *SQLiteStore) VerificationToken(id string) (Verification, error) {
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
	now := time.Now().UTC()
	v := Verification{DomainID: d.ID, TXTName: "_stackforge-verify." + d.Domain, Token: token, TokenHash: hex.EncodeToString(hash[:]), CreatedAt: now}
	_, err := s.db.Exec(`INSERT INTO domain_verifications (id, domain_id, txt_name, token_hash, created_at, verified_at)
VALUES (?,?,?,?,?,NULL)
ON CONFLICT(domain_id) DO UPDATE SET id=excluded.id, txt_name=excluded.txt_name, token_hash=excluded.token_hash, created_at=excluded.created_at, verified_at=NULL`, tokenID(), v.DomainID, v.TXTName, v.TokenHash, now.Unix())
	return v, err
}

func (s *SQLiteStore) MarkVerified(id string, token string) error {
	d, ok := s.Get(id)
	if !ok {
		return errors.New("domain not found")
	}
	var hash string
	err := s.db.QueryRow(`SELECT token_hash FROM domain_verifications WHERE domain_id=?`, d.ID).Scan(&hash)
	if err != nil {
		return errors.New("verification token not generated")
	}
	got := sha256.Sum256([]byte(token))
	if hex.EncodeToString(got[:]) != hash {
		return errors.New("verification token mismatch")
	}
	now := time.Now().UTC().Unix()
	_, err = s.db.Exec(`UPDATE domains SET ownership_status='verified', updated_at=? WHERE id=?`, now, d.ID)
	if err != nil {
		return err
	}
	_, _ = s.db.Exec(`UPDATE domain_verifications SET verified_at=? WHERE domain_id=?`, now, d.ID)
	return nil
}

func (s *SQLiteStore) migrate(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, sqliteSchema)
	return err
}

type sqliteScanner interface {
	Scan(dest ...any) error
}

func scanSQLiteDomain(row sqliteScanner) (Domain, error) {
	var d Domain
	var zone, record, value, last sql.NullString
	var createdAt, updatedAt int64
	var deletedAt sql.NullInt64
	err := row.Scan(&d.ID, &d.TenantID, &d.Domain, &d.RootDomain, &d.Subdomain, &d.TargetServiceName, &d.TargetServicePort, &d.Environment, &d.Provider, &zone, &record, &d.DNSRecordType, &value, &d.Proxied, &d.SSLEnabled, &d.OwnershipStatus, &d.RoutingStatus, &d.DeploymentStatus, &last, &createdAt, &updatedAt, &deletedAt)
	if err != nil {
		return Domain{}, err
	}
	d.ProviderZoneID = zone.String
	d.ProviderRecordID = record.String
	d.DNSRecordValue = value.String
	d.LastError = last.String
	d.CreatedAt = time.Unix(createdAt, 0).UTC()
	d.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	if deletedAt.Valid {
		t := time.Unix(deletedAt.Int64, 0).UTC()
		d.DeletedAt = &t
	}
	return d, nil
}

func emptyToNil(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}

const sqliteSchema = `
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
  proxied BOOLEAN NOT NULL DEFAULT 0,
  ssl_enabled BOOLEAN NOT NULL DEFAULT 1,
  ownership_status TEXT NOT NULL DEFAULT 'pending',
  routing_status TEXT NOT NULL DEFAULT 'pending',
  deployment_status TEXT NOT NULL DEFAULT 'pending',
  last_error TEXT,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  deleted_at INTEGER NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS domains_active_domain_unique ON domains(domain) WHERE deleted_at IS NULL;
CREATE TABLE IF NOT EXISTS domain_verifications (
  id TEXT PRIMARY KEY,
  domain_id TEXT NOT NULL REFERENCES domains(id),
  txt_name TEXT NOT NULL,
  token_hash TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  verified_at INTEGER NULL,
  UNIQUE(domain_id)
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
  created_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS reconcile_locks (
  domain_id TEXT PRIMARY KEY,
  holder TEXT NOT NULL,
  expires_at INTEGER NOT NULL,
  created_at INTEGER NOT NULL
);`

var _ Repository = (*SQLiteStore)(nil)

func (s *SQLiteStore) String() string {
	return fmt.Sprintf("sqlite-store(%p)", s.db)
}
