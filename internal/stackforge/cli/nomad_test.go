package cli

import (
	"reflect"
	"strings"
	"testing"

	"stackforge/internal/stackforge/inventory"
)

func TestSelectNomadNodePrefersNomadServer(t *testing.T) {
	inv := &inventory.Inventory{Nodes: []inventory.Node{
		{Name: "node-1", Roles: []string{"control-plane"}},
		{Name: "node-2", Roles: []string{"nomad-server"}},
	}}
	n, err := selectNomadNode(inv)
	if err != nil {
		t.Fatalf("selectNomadNode returned error: %v", err)
	}
	if n.Name != "node-2" {
		t.Fatalf("expected node-2, got %q", n.Name)
	}
}

func TestSelectNomadNodeFallsBackToFirstNode(t *testing.T) {
	inv := &inventory.Inventory{Nodes: []inventory.Node{
		{Name: "node-1", Roles: []string{"control-plane"}},
		{Name: "node-2", Roles: []string{"database"}},
	}}
	n, err := selectNomadNode(inv)
	if err != nil {
		t.Fatalf("selectNomadNode returned error: %v", err)
	}
	if n.Name != "node-1" {
		t.Fatalf("expected node-1 fallback, got %q", n.Name)
	}
}

func TestNomadAddressForRead(t *testing.T) {
	inv := &inventory.Inventory{NomadEndpoints: []string{"http://10.0.0.10:4646"}}
	if got := nomadAddressForRead(inv, "http://override:4646"); got != "http://override:4646" {
		t.Fatalf("expected override address, got %q", got)
	}
	if got := nomadAddressForRead(inv, ""); got != "http://10.0.0.10:4646" {
		t.Fatalf("expected inventory endpoint, got %q", got)
	}
	if got := nomadAddressForRead(&inventory.Inventory{}, ""); got != "http://127.0.0.1:4646" {
		t.Fatalf("expected localhost fallback, got %q", got)
	}
}

func TestBuildNomadReadRemoteCommand(t *testing.T) {
	cmd := buildNomadReadRemoteCommand("http://10.0.0.10:4646", "/v1/jobs", "default", "global")
	for _, want := range []string{"NOMAD_NAMESPACE", "NOMAD_REGION", "curl -fsS", "/v1/jobs"} {
		if !strings.Contains(cmd, want) {
			t.Fatalf("expected command to contain %q, got %q", want, cmd)
		}
	}
}

func TestDecodeNomadPayload(t *testing.T) {
	jsonRaw := "[{\"ID\":\"job-1\"}]"
	decoded := decodeNomadPayload(jsonRaw)
	arr, ok := decoded.([]any)
	if !ok || len(arr) != 1 {
		t.Fatalf("expected decoded json array, got %#v", decoded)
	}

	text := "10.0.0.10:4647"
	if got := decodeNomadPayload(text); got != text {
		t.Fatalf("expected plain text payload, got %#v", got)
	}

	empty := decodeNomadPayload("   ")
	if !reflect.DeepEqual(empty, map[string]any{}) {
		t.Fatalf("expected empty map payload, got %#v", empty)
	}
}

func TestNomadRemoteJobPath(t *testing.T) {
	got := nomadRemoteJobPath("/tmp/My App Job.hcl")
	if !strings.HasPrefix(got, "/tmp/stackforge-nomad-") || !strings.HasSuffix(got, ".hcl") {
		t.Fatalf("unexpected remote path format: %q", got)
	}
}

func TestBuildNomadJobRemoteCommand(t *testing.T) {
	cmd := buildNomadJobRemoteCommand("http://10.0.0.10:4646", "default", "global", "plan", "/tmp/job.hcl")
	for _, want := range []string{"NOMAD_NAMESPACE", "NOMAD_REGION", "nomad job plan", "-address='http://10.0.0.10:4646'", "/tmp/job.hcl"} {
		if !strings.Contains(cmd, want) {
			t.Fatalf("expected command to contain %q, got %q", want, cmd)
		}
	}
}

func TestBuildNomadJobStopRemoteCommand(t *testing.T) {
	cmd := buildNomadJobStopRemoteCommand("http://10.0.0.10:4646", "default", "global", "api-service")
	for _, want := range []string{"NOMAD_NAMESPACE", "NOMAD_REGION", "nomad job stop -yes", "api-service"} {
		if !strings.Contains(cmd, want) {
			t.Fatalf("expected command to contain %q, got %q", want, cmd)
		}
	}
}

func TestBuildNomadAllocRemoteCommand(t *testing.T) {
	t.Setenv("NOMAD_TOKEN", "token-123")
	statusCmd := buildNomadAllocRemoteCommand("http://10.0.0.10:4646", "default", "global", "status", "alloc-1", "")
	for _, want := range []string{"NOMAD_TOKEN", "NOMAD_NAMESPACE", "NOMAD_REGION", "nomad alloc status", "-json", "alloc-1"} {
		if !strings.Contains(statusCmd, want) {
			t.Fatalf("expected status command to contain %q, got %q", want, statusCmd)
		}
	}
	logsCmd := buildNomadAllocRemoteCommand("http://10.0.0.10:4646", "", "", "logs", "alloc-1", "api")
	for _, want := range []string{"nomad alloc logs", "-task", "api", "alloc-1"} {
		if !strings.Contains(logsCmd, want) {
			t.Fatalf("expected logs command to contain %q, got %q", want, logsCmd)
		}
	}
}

func TestEnforceNomadLiveSafetyRequiresConfirmProduction(t *testing.T) {
	orig := rootOpts
	t.Cleanup(func() { rootOpts = orig })

	rootOpts = orig
	rootOpts.confirmProduction = false
	rootOpts.yes = true

	inv := &inventory.Inventory{Environment: "production"}
	err := enforceNomadLiveSafety(inv, "nomad job run demo")
	if err == nil || !strings.Contains(err.Error(), "--confirm-production") {
		t.Fatalf("expected production confirmation error, got %v", err)
	}
}

func TestEnforceNomadLiveSafetyAllowsYesInNonProduction(t *testing.T) {
	orig := rootOpts
	t.Cleanup(func() { rootOpts = orig })

	rootOpts = orig
	rootOpts.confirmProduction = false
	rootOpts.yes = true

	inv := &inventory.Inventory{Environment: "staging"}
	if err := enforceNomadLiveSafety(inv, "nomad job run demo"); err != nil {
		t.Fatalf("expected no error for non-production with --yes, got %v", err)
	}
}

func TestBuildNomadDrainRemoteCommand(t *testing.T) {
	t.Setenv("NOMAD_TOKEN", "token-123")
	enable := buildNomadDrainRemoteCommand("http://10.0.0.10:4646", "global", "node-1", false)
	for _, want := range []string{"NOMAD_TOKEN", "NOMAD_REGION", "nomad node drain -enable", "node-1"} {
		if !strings.Contains(enable, want) {
			t.Fatalf("expected enable command to contain %q, got %q", want, enable)
		}
	}
	disable := buildNomadDrainRemoteCommand("http://10.0.0.10:4646", "global", "node-1", true)
	if !strings.Contains(disable, "nomad node drain -disable") {
		t.Fatalf("expected disable command, got %q", disable)
	}
}
