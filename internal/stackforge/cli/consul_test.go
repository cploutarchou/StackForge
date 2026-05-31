package cli

import (
	"strings"
	"testing"

	"stackforge/internal/stackforge/inventory"
	"stackforge/internal/stackforge/remoteexec"
)

func TestSelectConsulNodePrefersConsulServer(t *testing.T) {
	inv := &inventory.Inventory{Nodes: []inventory.Node{
		{Name: "node-1", Roles: []string{"control-plane"}},
		{Name: "node-2", Roles: []string{"consul-server"}},
	}}
	n, err := selectConsulNode(inv)
	if err != nil {
		t.Fatalf("selectConsulNode returned error: %v", err)
	}
	if n.Name != "node-2" {
		t.Fatalf("expected node-2, got %q", n.Name)
	}
}

func TestConsulAddressForRead(t *testing.T) {
	inv := &inventory.Inventory{ConsulEndpoints: []string{"http://10.0.0.20:8500"}}
	if got := consulAddressForRead(inv, "http://override:8500"); got != "http://override:8500" {
		t.Fatalf("expected override address, got %q", got)
	}
	if got := consulAddressForRead(inv, ""); got != "http://10.0.0.20:8500" {
		t.Fatalf("expected inventory endpoint, got %q", got)
	}
	if got := consulAddressForRead(&inventory.Inventory{}, ""); got != "http://127.0.0.1:8500" {
		t.Fatalf("expected localhost fallback, got %q", got)
	}
}

func TestEnforceConsulLiveSafetyRequiresConfirmProduction(t *testing.T) {
	orig := rootOpts
	t.Cleanup(func() { rootOpts = orig })
	rootOpts = orig
	rootOpts.confirmProduction = false
	rootOpts.yes = true

	inv := &inventory.Inventory{Environment: "production"}
	err := enforceConsulLiveSafety(inv, "consul kv put test")
	if err == nil || !strings.Contains(err.Error(), "--confirm-production") {
		t.Fatalf("expected production confirmation error, got %v", err)
	}
}

func TestURLQueryEscape(t *testing.T) {
	got := urlQueryEscape("dc 1+a&b=c")
	want := "dc%201%2Ba%26b%3Dc"
	if got != want {
		t.Fatalf("unexpected escaped value: got=%q want=%q", got, want)
	}
}

func TestBuildConsulCurlCommandIncludesToken(t *testing.T) {
	t.Setenv("CONSUL_HTTP_TOKEN", "secret-token")
	cmd, secrets := buildConsulCurlCommand("http://127.0.0.1:8500/v1/status/leader", "GET", "")
	if !strings.Contains(cmd, "X-Consul-Token") {
		t.Fatalf("expected token header in command, got %q", cmd)
	}
	if len(secrets) != 1 || secrets[0] != "secret-token" {
		t.Fatalf("expected token in secrets, got %+v", secrets)
	}
}

func TestBuildConsulSnapshotCommand(t *testing.T) {
	t.Setenv("CONSUL_HTTP_TOKEN", "secret-token")
	cmd, secrets := buildConsulSnapshotCommand("save", "/tmp/snap.snap", "http://127.0.0.1:8500", "dc1")
	for _, want := range []string{"CONSUL_HTTP_TOKEN", "consul snapshot save", "-http-addr='http://127.0.0.1:8500'", "-datacenter='dc1'", "/tmp/snap.snap"} {
		if !strings.Contains(cmd, want) {
			t.Fatalf("expected snapshot command to contain %q, got %q", want, cmd)
		}
	}
	if len(secrets) != 1 || secrets[0] != "secret-token" {
		t.Fatalf("expected token in snapshot secrets, got %+v", secrets)
	}
}

func TestNormalizeRemoteErrorIncludesStderr(t *testing.T) {
	err := normalizeRemoteError("consul kv put", "node-1", remoteexec.Result{Stderr: "permission denied"}, assertErr("boom"))
	if err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("expected normalized stderr in error, got %v", err)
	}
}

type assertErr string

func (e assertErr) Error() string { return string(e) }
