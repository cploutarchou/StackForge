package secrets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadOrGenerateWrites0600(t *testing.T) {
	path := filepath.Join(t.TempDir(), "generated-secrets.yaml")
	_, created, err := LoadOrGenerate(path, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("expected created")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("mode = %v", info.Mode().Perm())
	}
}

func TestLoadOrGenerateDoesNotUseGeneratedByInstallAsAdminKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "generated-secrets.yaml")
	sec, created, err := LoadOrGenerate(path, []string{"generated-by-install"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("expected created")
	}
	if sec.StackForgeAdminAPIKey == "generated-by-install" || sec.StackForgeAdminAPIKey == "" {
		t.Fatalf("expected generated admin API key, got %q", sec.StackForgeAdminAPIKey)
	}
}

func TestDeployEnvCommandRedactsEncodedPayloadAndSecrets(t *testing.T) {
	s := &Secrets{StackForgeAdminAPIKey: "admin-secret", DatabasePassword: "db-secret", InternalServiceToken: "svc-secret"}
	env := s.Env("postgres://stackforge:db-secret@127.0.0.1:5432/stackforge")
	cmd := DeployEnvCommand(env, s.Values())
	if cmd.Command == "" || !cmd.Sudo {
		t.Fatal("expected sudo deployment command")
	}
	log := cmd.Command + " admin-secret db-secret"
	redacted := RedactAll(log, cmd.Secrets)
	if redacted == log {
		t.Fatal("expected command log to be redacted")
	}
	for _, secret := range []string{"admin-secret", "db-secret"} {
		if strings.Contains(redacted, secret) {
			t.Fatalf("secret leaked after redaction: %s", secret)
		}
	}
}
