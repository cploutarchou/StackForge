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
