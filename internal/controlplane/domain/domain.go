package domain

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strings"
	"sync"
	"time"
)

type Domain struct {
	ID                string     `json:"id"`
	TenantID          string     `json:"tenant_id"`
	Domain            string     `json:"domain"`
	RootDomain        string     `json:"root_domain"`
	Subdomain         string     `json:"subdomain"`
	TargetServiceName string     `json:"target_service_name"`
	TargetServicePort int        `json:"target_service_port"`
	Environment       string     `json:"environment"`
	Provider          string     `json:"provider"`
	ProviderZoneID    string     `json:"provider_zone_id"`
	ProviderRecordID  string     `json:"provider_record_id"`
	DNSRecordType     string     `json:"dns_record_type"`
	DNSRecordValue    string     `json:"dns_record_value"`
	Proxied           bool       `json:"proxied"`
	SSLEnabled        bool       `json:"ssl_enabled"`
	OwnershipStatus   string     `json:"ownership_status"`
	RoutingStatus     string     `json:"routing_status"`
	DeploymentStatus  string     `json:"deployment_status"`
	LastError         string     `json:"last_error"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
	DeletedAt         *time.Time `json:"deleted_at,omitempty"`
}

type Verification struct {
	DomainID  string    `json:"domain_id"`
	TXTName   string    `json:"txt_name"`
	TokenHash string    `json:"-"`
	Token     string    `json:"token,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type Repository interface {
	Create(d Domain, allowWildcard bool) (Domain, error)
	List() []Domain
	Get(id string) (Domain, bool)
	Delete(id string) error
	VerificationToken(id string) (Verification, error)
	MarkVerified(id string, token string) error
}

type Store struct {
	mu            sync.Mutex
	domains       map[string]Domain
	byDomain      map[string]string
	verifications map[string]Verification
}

func NewStore() *Store {
	return &Store{domains: map[string]Domain{}, byDomain: map[string]string{}, verifications: map[string]Verification{}}
}

func (s *Store) Create(d Domain, allowWildcard bool) (Domain, error) {
	if err := ValidateName(d.Domain, allowWildcard); err != nil {
		return Domain{}, err
	}
	if d.TenantID == "" || d.TargetServiceName == "" || d.TargetServicePort <= 0 {
		return Domain{}, errors.New("tenant_id, target_service_name, and target_service_port are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	d.Domain = strings.ToLower(strings.TrimSpace(d.Domain))
	if id, exists := s.byDomain[d.Domain]; exists && s.domains[id].DeletedAt == nil {
		return Domain{}, fmt.Errorf("domain %s already exists", d.Domain)
	}
	now := time.Now().UTC()
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
	s.domains[d.ID] = d
	s.byDomain[d.Domain] = d.ID
	return d, nil
}

func (s *Store) List() []Domain {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Domain{}
	for _, d := range s.domains {
		if d.DeletedAt == nil {
			out = append(out, d)
		}
	}
	return out
}

func (s *Store) Get(id string) (Domain, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.domains[id]
	return d, ok && d.DeletedAt == nil
}

func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.domains[id]
	if !ok {
		return errors.New("not found")
	}
	now := time.Now().UTC()
	d.DeletedAt = &now
	d.RoutingStatus = "disabled"
	d.UpdatedAt = now
	s.domains[id] = d
	return nil
}

func (s *Store) VerificationToken(id string) (Verification, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.domains[id]
	if !ok || d.DeletedAt != nil {
		return Verification{}, errors.New("domain not found")
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return Verification{}, err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	hash := sha256.Sum256([]byte(token))
	v := Verification{DomainID: id, TXTName: "_stackforge-verify." + d.Domain, Token: token, TokenHash: hex.EncodeToString(hash[:]), CreatedAt: time.Now().UTC()}
	s.verifications[id] = v
	return v, nil
}

func (s *Store) MarkVerified(id string, token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.verifications[id]
	if !ok {
		return errors.New("verification token not generated")
	}
	hash := sha256.Sum256([]byte(token))
	if hex.EncodeToString(hash[:]) != v.TokenHash {
		return errors.New("verification token mismatch")
	}
	d := s.domains[id]
	d.OwnershipStatus = "verified"
	d.UpdatedAt = time.Now().UTC()
	s.domains[id] = d
	return nil
}

var labelRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

func ValidateName(name string, allowWildcard bool) error {
	name = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(name), "."))
	if name == "" || name == "localhost" || !strings.Contains(name, ".") {
		return errors.New("invalid public domain")
	}
	if ip := net.ParseIP(name); ip != nil {
		return errors.New("IP addresses are not valid domains")
	}
	if strings.HasPrefix(name, "*.") && !allowWildcard {
		return errors.New("wildcard domains are disabled")
	}
	if strings.HasSuffix(name, ".local") || strings.HasSuffix(name, ".internal") || strings.HasSuffix(name, ".localhost") {
		return errors.New("internal hostnames are rejected")
	}
	for _, part := range strings.Split(strings.TrimPrefix(name, "*."), ".") {
		if !labelRe.MatchString(part) {
			return fmt.Errorf("invalid domain label %q", part)
		}
	}
	return nil
}

func rootDomain(d string) string {
	parts := strings.Split(d, ".")
	if len(parts) < 2 {
		return d
	}
	return strings.Join(parts[len(parts)-2:], ".")
}

func subdomain(d string) string {
	parts := strings.Split(d, ".")
	if len(parts) <= 2 {
		return ""
	}
	return strings.Join(parts[:len(parts)-2], ".")
}

func tokenID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
