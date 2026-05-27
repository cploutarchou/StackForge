package domain

import (
	"context"
	"path/filepath"
	"testing"
)

func TestSQLiteStoreCreateAndVerify(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "stackforge.db")
	store, err := NewSQLiteStore(context.Background(), "sqlite://"+dbPath)
	if err != nil {
		t.Fatalf("new sqlite store: %v", err)
	}
	d, err := store.Create(Domain{TenantID: "tenant-1", Domain: "app.example.com", TargetServiceName: "app", TargetServicePort: 8080}, false)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if d.ID == "" {
		t.Fatal("expected created domain id")
	}
	v, err := store.VerificationToken(d.ID)
	if err != nil {
		t.Fatalf("verification token: %v", err)
	}
	if err := store.MarkVerified(d.ID, v.Token); err != nil {
		t.Fatalf("mark verified: %v", err)
	}
	got, ok := store.Get(d.ID)
	if !ok {
		t.Fatal("expected stored domain")
	}
	if got.OwnershipStatus != "verified" {
		t.Fatalf("expected verified ownership status, got %q", got.OwnershipStatus)
	}
}
