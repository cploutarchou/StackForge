package domainpool

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"stackforge/internal/controlplane/dns/cloudflare"
)

type fakeResolver struct {
	hosts []string
	cname string
}

func (f fakeResolver) LookupHost(ctx context.Context, host string) ([]string, error) {
	return f.hosts, nil
}

func (f fakeResolver) LookupCNAME(ctx context.Context, host string) (string, error) {
	return f.cname, nil
}

func TestAddListRemoveDuplicate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pool.yaml")
	e, err := Add(path, Entry{Domain: "example.com", TargetType: "traefik", TargetValue: "203.0.113.10", RecordType: "A"}, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if e.RootDomain != "example.com" || e.Provider != "cloudflare" {
		t.Fatalf("unexpected entry: %+v", e)
	}
	if _, err := Add(path, Entry{Domain: "example.com", TargetType: "traefik", TargetValue: "203.0.113.10", RecordType: "A"}, false, false); err == nil {
		t.Fatal("expected duplicate rejection")
	}
	removed, err := Remove(path, "example.com")
	if err != nil {
		t.Fatal(err)
	}
	if removed.Status != "disabled" {
		t.Fatalf("expected disabled status, got %s", removed.Status)
	}
}

func TestMissingCloudflareTokenFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pool.yaml")
	if _, err := Add(path, Entry{Domain: "example.com", TargetType: "traefik", TargetValue: "203.0.113.10", RecordType: "A"}, false, false); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLOUDFLARE_API_TOKEN", "")
	_, err := ApplyDNS(context.Background(), "example.com", ApplyOptions{Path: path, AuditPath: filepath.Join(filepath.Dir(path), "audit.jsonl")})
	if err == nil || !strings.Contains(err.Error(), "CLOUDFLARE_API_TOKEN") {
		t.Fatalf("expected missing token error, got %v", err)
	}
}

func TestApplyDNSWithMockedCloudflareAndAudit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pool.yaml")
	auditPath := filepath.Join(filepath.Dir(path), "audit.jsonl")
	if _, err := Add(path, Entry{Domain: "app.example.com", TargetType: "control-plane", TargetValue: "edge.example.com", RecordType: "CNAME"}, false, false); err != nil {
		t.Fatal(err)
	}
	var created bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/zones":
			_, _ = w.Write([]byte(`{"success":true,"result":[{"id":"zone1","name":"example.com"}]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/zones/zone1/dns_records":
			if created {
				_, _ = w.Write([]byte(`{"success":true,"result":[{"id":"rec1","type":"CNAME","name":"app.example.com","content":"edge.example.com","proxied":false}]}`))
				return
			}
			_, _ = w.Write([]byte(`{"success":true,"result":[]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/zones/zone1/dns_records":
			created = true
			_, _ = w.Write([]byte(`{"success":true,"result":{"id":"rec1"}}`))
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()
	e, err := ApplyDNS(context.Background(), "app.example.com", ApplyOptions{Path: path, AuditPath: auditPath, Client: &cloudflare.Client{Token: "token", BaseURL: srv.URL, HTTPClient: srv.Client()}})
	if err != nil {
		t.Fatal(err)
	}
	if e.DNSStatus != "applied" || e.ProviderRecordID != "rec1" {
		t.Fatalf("unexpected applied entry: %+v", e)
	}
	if b, err := os.ReadFile(auditPath); err != nil || !strings.Contains(string(b), "domain_pool.apply_dns") {
		t.Fatalf("expected audit log, err=%v body=%s", err, b)
	}
}

func TestVerifyDNSMocked(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pool.yaml")
	if _, err := Add(path, Entry{Domain: "example.com", TargetType: "traefik", TargetValue: "203.0.113.10", RecordType: "A"}, false, false); err != nil {
		t.Fatal(err)
	}
	e, err := VerifyDNS(context.Background(), path, "example.com", fakeResolver{hosts: []string{"203.0.113.10"}})
	if err != nil {
		t.Fatal(err)
	}
	if e.DNSStatus != "verified" || e.VerificationStatus != "verified" {
		t.Fatalf("unexpected verification status: %+v", e)
	}
}
