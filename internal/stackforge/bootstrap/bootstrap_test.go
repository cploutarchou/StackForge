package bootstrap

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"stackforge/internal/stackforge/remoteexec"
)

type fakeExec struct {
	commands []remoteexec.Command
	fail     bool
}

func (f *fakeExec) Run(ctx context.Context, node string, cmd remoteexec.Command) (remoteexec.Result, error) {
	f.commands = append(f.commands, cmd)
	if f.fail {
		return remoteexec.Result{Stderr: "denied"}, context.DeadlineExceeded
	}
	return remoteexec.Result{}, nil
}

func TestValidatePublicKeyFileMissing(t *testing.T) {
	if _, err := ValidatePublicKeyFile(filepath.Join(t.TempDir(), "missing.pub")); err == nil {
		t.Fatal("expected missing file error")
	}
}

func TestValidatePublicKeyFileInvalid(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.pub")
	if err := os.WriteFile(path, []byte("not-a-key"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidatePublicKeyFile(path); err == nil {
		t.Fatal("expected invalid public key error")
	}
}

func TestAuthorizedKeysAppendCommandIsIdempotent(t *testing.T) {
	cmd := AuthorizedKeysAppendCommand(testPublicKey(t))
	for _, want := range []string{"grep -qxF", "authorized_keys", "chmod 0600"} {
		if !strings.Contains(cmd, want) {
			t.Fatalf("missing %q in %s", want, cmd)
		}
	}
}

func TestPasswordNeverAppearsInReportOrInventory(t *testing.T) {
	keyPath := filepath.Join(t.TempDir(), "id.pub")
	pub := testPublicKey(t)
	if err := os.WriteFile(keyPath, []byte(pub), 0600); err != nil {
		t.Fatal(err)
	}
	state := t.TempDir()
	passExec := &fakeExec{}
	keyExec := &fakeExec{}
	report, err := Run(context.Background(), Options{
		ClusterName:    "test",
		Environment:    "staging",
		StateDir:       state,
		Nodes:          []Node{{Name: "node-1", Address: "203.0.113.10", User: "root", Port: 22, Auth: AuthPassword, PublicKeyPath: keyPath}},
		PasswordReader: func(Node) (string, error) { return "secret-password", nil },
		PasswordExecutorFactory: func(Node, string) remoteexec.Executor {
			return passExec
		},
		KeyExecutorFactory: func(Node) remoteexec.Executor {
			return keyExec
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(report.ReportPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "secret-password") {
		t.Fatal("password leaked into report")
	}
	inv, err := os.ReadFile(filepath.Join(state, "inventory.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(inv), "secret-password") {
		t.Fatal("password leaked into inventory")
	}
	if len(passExec.commands) == 0 || len(keyExec.commands) == 0 {
		t.Fatal("expected password copy and key verification commands")
	}
}

func TestKeyVerificationFailureIsClear(t *testing.T) {
	keyPath := filepath.Join(t.TempDir(), "id.pub")
	pub := testPublicKey(t)
	if err := os.WriteFile(keyPath, []byte(pub), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := Run(context.Background(), Options{
		ClusterName:    "test",
		Environment:    "staging",
		StateDir:       t.TempDir(),
		Nodes:          []Node{{Name: "node-1", Address: "203.0.113.10", User: "root", Port: 22, Auth: AuthPassword, PublicKeyPath: keyPath}},
		PasswordReader: func(Node) (string, error) { return "secret-password", nil },
		PasswordExecutorFactory: func(Node, string) remoteexec.Executor {
			return &fakeExec{}
		},
		KeyExecutorFactory: func(Node) remoteexec.Executor {
			return &fakeExec{fail: true}
		},
	})
	if err == nil || !strings.Contains(err.Error(), "key-based SSH verification failed") {
		t.Fatalf("expected clear key verification error, got %v", err)
	}
}

func testPublicKey(t *testing.T) string {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub)))
}
