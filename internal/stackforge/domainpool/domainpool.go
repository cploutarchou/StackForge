package domainpool

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"stackforge/internal/controlplane/dns/cloudflare"
	cpdomain "stackforge/internal/controlplane/domain"
)

type Entry struct {
	ID                 string    `yaml:"id" json:"id"`
	Domain             string    `yaml:"domain" json:"domain"`
	RootDomain         string    `yaml:"root_domain" json:"root_domain"`
	ZoneID             string    `yaml:"zone_id" json:"zone_id"`
	Provider           string    `yaml:"provider" json:"provider"`
	ProviderRecordID   string    `yaml:"provider_record_id,omitempty" json:"provider_record_id,omitempty"`
	Status             string    `yaml:"status" json:"status"`
	DNSStatus          string    `yaml:"dns_status" json:"dns_status"`
	VerificationStatus string    `yaml:"verification_status" json:"verification_status"`
	TargetType         string    `yaml:"target_type" json:"target_type"`
	TargetValue        string    `yaml:"target_value" json:"target_value"`
	RecordType         string    `yaml:"record_type" json:"record_type"`
	Proxied            bool      `yaml:"proxied" json:"proxied"`
	CreatedAt          time.Time `yaml:"created_at" json:"created_at"`
	UpdatedAt          time.Time `yaml:"updated_at" json:"updated_at"`
	LastError          string    `yaml:"last_error,omitempty" json:"last_error,omitempty"`
}

type Store struct {
	Entries []Entry `yaml:"entries" json:"entries"`
}

type Resolver interface {
	LookupHost(ctx context.Context, host string) ([]string, error)
	LookupCNAME(ctx context.Context, host string) (string, error)
}

type NetResolver struct{}

func (NetResolver) LookupHost(ctx context.Context, host string) ([]string, error) {
	return net.DefaultResolver.LookupHost(ctx, host)
}

func (NetResolver) LookupCNAME(ctx context.Context, host string) (string, error) {
	return net.DefaultResolver.LookupCNAME(ctx, host)
}

type ApplyOptions struct {
	Path      string
	AuditPath string
	Client    *cloudflare.Client
	DryRun    bool
	TokenEnv  string
}

func Load(path string) (*Store, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Store{Entries: []Entry{}}, nil
		}
		return nil, err
	}
	var s Store
	if err := yaml.Unmarshal(b, &s); err != nil {
		return nil, err
	}
	if s.Entries == nil {
		s.Entries = []Entry{}
	}
	return &s, nil
}

func Save(path string, s *Store) error {
	if s == nil {
		s = &Store{}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	b, err := yaml.Marshal(s)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0600)
}

func Add(path string, e Entry, allowInternal bool, allowWildcard bool) (Entry, error) {
	s, err := Load(path)
	if err != nil {
		return Entry{}, err
	}
	if err := ValidateEntry(e, allowInternal, allowWildcard); err != nil {
		return Entry{}, err
	}
	e.Domain = normalize(e.Domain)
	for _, current := range s.Entries {
		if current.Domain == e.Domain && current.Status != "disabled" {
			return Entry{}, fmt.Errorf("domain %s already exists in pool", e.Domain)
		}
	}
	now := time.Now().UTC()
	e.ID = newID()
	e.RootDomain = rootDomain(e.Domain)
	if e.Provider == "" {
		e.Provider = "cloudflare"
	}
	if e.Status == "" {
		e.Status = "pending"
	}
	if e.DNSStatus == "" {
		e.DNSStatus = "pending"
	}
	if e.VerificationStatus == "" {
		e.VerificationStatus = "pending"
	}
	if e.RecordType == "" {
		e.RecordType = "A"
	}
	e.RecordType = strings.ToUpper(e.RecordType)
	e.CreatedAt = now
	e.UpdatedAt = now
	s.Entries = append(s.Entries, e)
	if err := Save(path, s); err != nil {
		return Entry{}, err
	}
	return e, nil
}

func Remove(path, domain string) (Entry, error) {
	s, err := Load(path)
	if err != nil {
		return Entry{}, err
	}
	for i := range s.Entries {
		if s.Entries[i].Domain == normalize(domain) || s.Entries[i].ID == domain {
			s.Entries[i].Status = "disabled"
			s.Entries[i].UpdatedAt = time.Now().UTC()
			if err := Save(path, s); err != nil {
				return Entry{}, err
			}
			return s.Entries[i], nil
		}
	}
	return Entry{}, errors.New("domain not found")
}

func Find(path, domain string) (Entry, int, *Store, error) {
	s, err := Load(path)
	if err != nil {
		return Entry{}, -1, nil, err
	}
	for i, e := range s.Entries {
		if e.Domain == normalize(domain) || e.ID == domain {
			return e, i, s, nil
		}
	}
	return Entry{}, -1, s, errors.New("domain not found")
}

func ValidateEntry(e Entry, allowInternal bool, allowWildcard bool) error {
	if err := cpdomain.ValidateName(e.Domain, allowWildcard); err != nil {
		return err
	}
	if !allowInternal {
		if ip := net.ParseIP(e.TargetValue); ip != nil && (ip.IsPrivate() || ip.IsLoopback() || ip.IsUnspecified()) {
			return errors.New("internal target IPs are rejected unless explicitly enabled")
		}
	}
	switch strings.ToLower(e.TargetType) {
	case "traefik", "control-plane", "custom":
	default:
		return fmt.Errorf("target_type must be traefik, control-plane, or custom")
	}
	switch strings.ToUpper(e.RecordType) {
	case "A":
		if net.ParseIP(e.TargetValue) == nil {
			return errors.New("A records require an IP target_value")
		}
	case "CNAME":
		if err := cpdomain.ValidateName(e.TargetValue, false); err != nil {
			return fmt.Errorf("CNAME target_value must be a public domain: %w", err)
		}
	default:
		return errors.New("record_type must be A or CNAME")
	}
	return nil
}

func ApplyDNS(ctx context.Context, domain string, opts ApplyOptions) (Entry, error) {
	e, idx, s, err := Find(opts.Path, domain)
	if err != nil {
		return Entry{}, err
	}
	before := e
	if opts.DryRun {
		e.LastError = ""
		audit(opts.AuditPath, "domain_pool.apply_dns.dry_run", e.ID, before, e, "")
		return e, nil
	}
	client := opts.Client
	if client == nil {
		tokenEnv := opts.TokenEnv
		if tokenEnv == "" {
			tokenEnv = "CLOUDFLARE_API_TOKEN"
		}
		token := os.Getenv(tokenEnv)
		if token == "" {
			err := fmt.Errorf("%s is required for DNS apply", tokenEnv)
			e.LastError = err.Error()
			s.Entries[idx] = e
			_ = Save(opts.Path, s)
			audit(opts.AuditPath, "domain_pool.apply_dns", e.ID, before, e, err.Error())
			return e, err
		}
		client = &cloudflare.Client{Token: token}
	}
	if e.ZoneID == "" {
		zone, err := client.LookupZone(ctx, e.RootDomain)
		if err != nil {
			e.LastError = err.Error()
			s.Entries[idx] = e
			_ = Save(opts.Path, s)
			audit(opts.AuditPath, "domain_pool.apply_dns", e.ID, before, e, err.Error())
			return e, err
		}
		e.ZoneID = zone
	}
	rec := cloudflare.Record{Type: e.RecordType, Name: e.Domain, Content: e.TargetValue, Proxied: e.Proxied}
	if err := client.UpsertRecord(ctx, e.ZoneID, rec); err != nil {
		e.DNSStatus = "failed"
		e.Status = "failed"
		e.LastError = err.Error()
		s.Entries[idx] = e
		_ = Save(opts.Path, s)
		audit(opts.AuditPath, "domain_pool.apply_dns", e.ID, before, e, err.Error())
		return e, err
	}
	if records, err := client.ListRecords(ctx, e.ZoneID, e.Domain); err == nil {
		for _, r := range records {
			if r.Type == e.RecordType && r.Name == e.Domain {
				e.ProviderRecordID = r.ID
				break
			}
		}
	}
	e.DNSStatus = "applied"
	e.Status = "active"
	e.LastError = ""
	e.UpdatedAt = time.Now().UTC()
	s.Entries[idx] = e
	if err := Save(opts.Path, s); err != nil {
		return Entry{}, err
	}
	audit(opts.AuditPath, "domain_pool.apply_dns", e.ID, before, e, "")
	return e, nil
}

func VerifyDNS(ctx context.Context, path, domain string, resolver Resolver) (Entry, error) {
	if resolver == nil {
		resolver = NetResolver{}
	}
	e, idx, s, err := Find(path, domain)
	if err != nil {
		return Entry{}, err
	}
	before := e
	var verifyErr error
	switch e.RecordType {
	case "A":
		hosts, err := resolver.LookupHost(ctx, e.Domain)
		if err != nil {
			verifyErr = err
			break
		}
		want := net.ParseIP(e.TargetValue)
		found := false
		for _, host := range hosts {
			if net.ParseIP(host).Equal(want) {
				found = true
			}
		}
		if !found {
			verifyErr = fmt.Errorf("A record for %s has %v, want %s", e.Domain, hosts, e.TargetValue)
		}
	case "CNAME":
		got, err := resolver.LookupCNAME(ctx, e.Domain)
		if err != nil {
			verifyErr = err
			break
		}
		if trimDot(got) != trimDot(e.TargetValue) {
			verifyErr = fmt.Errorf("CNAME for %s is %s, want %s", e.Domain, got, e.TargetValue)
		}
	default:
		verifyErr = fmt.Errorf("unsupported record type %s", e.RecordType)
	}
	e.UpdatedAt = time.Now().UTC()
	if verifyErr != nil {
		e.DNSStatus = "failed"
		e.VerificationStatus = "failed"
		e.LastError = verifyErr.Error()
		s.Entries[idx] = e
		_ = Save(path, s)
		audit(filepath.Join(filepath.Dir(path), "domain-pool-audit.jsonl"), "domain_pool.verify_dns", e.ID, before, e, verifyErr.Error())
		return e, verifyErr
	}
	e.DNSStatus = "verified"
	e.VerificationStatus = "verified"
	e.LastError = ""
	s.Entries[idx] = e
	if err := Save(path, s); err != nil {
		return Entry{}, err
	}
	audit(filepath.Join(filepath.Dir(path), "domain-pool-audit.jsonl"), "domain_pool.verify_dns", e.ID, before, e, "")
	return e, nil
}

func audit(path, action, resourceID string, before, after Entry, errText string) {
	if path == "" {
		return
	}
	_ = os.MkdirAll(filepath.Dir(path), 0700)
	rec := map[string]any{"id": newID(), "actor": "stackforge-cli", "action": action, "resource_type": "domain_pool_entry", "resource_id": resourceID, "before": before, "after": after, "error": errText, "created_at": time.Now().UTC()}
	b, _ := json.Marshal(rec)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(b, '\n'))
}

func normalize(s string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(s), "."))
}

func rootDomain(d string) string {
	parts := strings.Split(normalize(d), ".")
	if len(parts) < 2 {
		return d
	}
	return strings.Join(parts[len(parts)-2:], ".")
}

func trimDot(s string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(s)), ".")
}

func newID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
