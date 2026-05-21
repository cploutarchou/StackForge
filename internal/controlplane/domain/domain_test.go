package domain

import "testing"

func TestValidateRejectsInternalAndIP(t *testing.T) {
	for _, d := range []string{"localhost", "127.0.0.1", "service.internal"} {
		if err := ValidateName(d, false); err == nil {
			t.Fatalf("expected reject for %s", d)
		}
	}
}

func TestStoreDuplicatePreventionAndVerification(t *testing.T) {
	s := NewStore()
	d, err := s.Create(Domain{TenantID: "t1", Domain: "Example.COM", TargetServiceName: "frontend", TargetServicePort: 8080}, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(Domain{TenantID: "t2", Domain: "example.com", TargetServiceName: "frontend", TargetServicePort: 8080}, false); err == nil {
		t.Fatal("expected duplicate reject")
	}
	v, err := s.VerificationToken(d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkVerified(d.ID, v.Token); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(d.ID)
	if got.OwnershipStatus != "verified" {
		t.Fatalf("status %s", got.OwnershipStatus)
	}
}
