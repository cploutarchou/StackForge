package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestContextReportWithoutConfig(t *testing.T) {
	orig := rootOpts
	t.Cleanup(func() { rootOpts = orig })

	rootOpts = orig
	rootOpts.configPath = ""
	rootOpts.stateDir = ""
	rootOpts.cluster = ""

	report := contextReport()

	if got, ok := report["config_loaded"].(bool); !ok || got {
		t.Fatalf("expected config_loaded=false, got %#v", report["config_loaded"])
	}
	if got := report["note"]; got == nil {
		t.Fatal("expected note when no config is provided")
	}
}

func TestContextReportWithConfig(t *testing.T) {
	orig := rootOpts
	t.Cleanup(func() { rootOpts = orig })

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "stackforge.yaml")
	cfg := "cluster:\n" +
		"  name: stackforge-staging\n" +
		"  environment: staging\n" +
		"  datacenter: dc1\n" +
		"ssh:\n" +
		"  user: ubuntu\n" +
		"  port: 2222\n" +
		"nodes:\n" +
		"  - name: node-1\n" +
		"    address: 10.0.0.10\n" +
		"    roles: [control-plane]\n" +
		"network:\n" +
		"  allowed_admin_cidrs: [10.0.0.0/8]\n" +
		"  allowed_ssh_cidrs: [10.0.0.0/8]\n" +
		"consul:\n" +
		"  version: latest-stable\n" +
		"nomad:\n" +
		"  version: latest-stable\n" +
		"traefik:\n" +
		"  version: latest-stable\n" +
		"  dashboard_enabled: false\n" +
		"database:\n" +
		"  engine: sqlite\n" +
		"control_plane:\n" +
		"  domain: stackforge.test\n" +
		"  api_port: 8080\n" +
		"  admin_api_keys: [\"secret\"]\n"
	if err := os.WriteFile(cfgPath, []byte(cfg), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	rootOpts = orig
	rootOpts.configPath = cfgPath
	rootOpts.stateDir = filepath.Join(dir, ".stackforge")
	rootOpts.cluster = ""
	rootOpts.confirmProduction = false
	rootOpts.allowExampleConfig = false
	rootOpts.allowPublicSSH = false

	report := contextReport()

	if got, ok := report["config_loaded"].(bool); !ok || !got {
		t.Fatalf("expected config_loaded=true, got %#v (config_error=%#v)", report["config_loaded"], report["config_error"])
	}
	if got := report["cluster_name"]; got != "stackforge-staging" {
		t.Fatalf("expected cluster_name stackforge-staging, got %#v", got)
	}
	if got := report["cluster_environment"]; got != "staging" {
		t.Fatalf("expected cluster_environment staging, got %#v", got)
	}
	if got := report["node_count"]; got != 1 {
		t.Fatalf("expected node_count 1, got %#v", got)
	}
	dns, ok := report["dns"].(map[string]any)
	if !ok {
		t.Fatalf("expected dns map, got %#v", report["dns"])
	}
	if got := dns["control_plane_domain"]; got != "stackforge.test" {
		t.Fatalf("expected control_plane_domain stackforge.test, got %#v", got)
	}
	safetyReport, ok := report["safety"].(map[string]any)
	if !ok {
		t.Fatalf("expected safety map, got %#v", report["safety"])
	}
	if got := safetyReport["safe"]; got != true {
		t.Fatalf("expected safety.safe=true, got %#v", got)
	}
}

func TestPrintContextText(t *testing.T) {
	report := map[string]any{
		"cluster":             "stackforge-staging",
		"config":              "/tmp/stackforge.yaml",
		"state_dir":           "/tmp/.stackforge/stackforge-staging",
		"output":              "text",
		"dry_run":             false,
		"yes":                 true,
		"verbose":             true,
		"log_level":           "debug",
		"no_color":            false,
		"allow_no_firewall":   false,
		"allow_example_cfg":   false,
		"allow_public_ssh":    false,
		"confirm_production":  true,
		"config_loaded":       true,
		"cluster_name":        "stackforge-staging",
		"cluster_environment": "staging",
		"cluster_datacenter":  "dc1",
		"node_count":          2,
		"ssh": map[string]any{
			"user":     "ubuntu",
			"port":     2222,
			"key_set":  true,
			"copy_key": false,
		},
		"dns": map[string]any{
			"control_plane_domain": "stackforge.test",
			"traefik_dashboard":    "traefik.stackforge.test",
			"cloudflare_zone_set":  true,
		},
		"network": map[string]any{
			"allowed_admin_cidrs":  []string{"10.0.0.0/8"},
			"allowed_ssh_cidrs":    []string{"10.0.0.0/8"},
			"public_internal_comm": false,
		},
		"components": map[string]any{
			"consul":   map[string]any{"version": "latest-stable", "acl_enabled": true, "encrypt_gossip": true},
			"nomad":    map[string]any{"version": "latest-stable", "acl_enabled": true, "encrypt_gossip": true},
			"traefik":  map[string]any{"version": "latest-stable", "dashboard_enabled": false, "dashboard_basic_auth": false},
			"database": map[string]any{"engine": "sqlite", "mode": "local", "backup_enabled": true},
		},
		"safety": map[string]any{
			"safe":       true,
			"production": false,
			"findings":   []any{},
		},
	}

	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	printContextText(report)
	w.Close()
	os.Stdout = oldStdout

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatalf("read output: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"cluster: stackforge-staging", "config_loaded: yes", "ssh: user=ubuntu port=2222 key_set=yes", "dns: control_plane=stackforge.test", "safety: production=no safe=yes"} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected output to contain %q, got:\n%s", want, out)
		}
	}
}

func TestBuildRemoteStackforgeArgsAddsDefaults(t *testing.T) {
	orig := rootOpts
	t.Cleanup(func() { rootOpts = orig })
	rootOpts.output = "json"
	rootOpts.confirmProduction = true

	args := buildRemoteStackforgeArgs([]string{"verify"}, "stackforge-cluster", "~/.stackforge")
	wantPrefix := []string{"--confirm-production", "--output", "json", "--state-dir", "~/.stackforge", "--cluster", "stackforge-cluster", "verify"}
	if !reflect.DeepEqual(args, wantPrefix) {
		t.Fatalf("unexpected args:\nwant=%v\n got=%v", wantPrefix, args)
	}
}

func TestBuildRemoteStackforgeArgsPreservesExplicitFlags(t *testing.T) {
	orig := rootOpts
	t.Cleanup(func() { rootOpts = orig })
	rootOpts.output = "json"
	rootOpts.confirmProduction = true

	args := buildRemoteStackforgeArgs([]string{"stackforge", "--cluster=custom", "--output", "text", "status"}, "stackforge-cluster", "~/.stackforge")
	if !reflect.DeepEqual(args, []string{"--confirm-production", "--state-dir", "~/.stackforge", "--cluster=custom", "--output", "text", "status"}) {
		t.Fatalf("unexpected args: %v", args)
	}
}

func TestHasFlag(t *testing.T) {
	if !hasFlag([]string{"--cluster", "abc", "status"}, "--cluster") {
		t.Fatal("expected --cluster to be detected")
	}
	if !hasFlag([]string{"--cluster=abc", "status"}, "--cluster") {
		t.Fatal("expected --cluster=abc to be detected")
	}
	if hasFlag([]string{"status", "cluster"}, "--cluster") {
		t.Fatal("did not expect --cluster to be detected")
	}
}

func TestSSHTarget(t *testing.T) {
	if got := sshTarget("root", "1.2.3.4"); got != "root@1.2.3.4" {
		t.Fatalf("unexpected target: %s", got)
	}
	if got := sshTarget("", "1.2.3.4"); got != "1.2.3.4" {
		t.Fatalf("unexpected target: %s", got)
	}
}

func TestResolveSyncDirection(t *testing.T) {
	tests := []struct {
		name      string
		direction string
		pull      bool
		push      bool
		want      string
		wantErr   bool
	}{
		{name: "default pull", direction: "pull", want: "pull"},
		{name: "direction push", direction: "push", want: "push"},
		{name: "pull flag overrides", direction: "push", pull: true, want: "pull"},
		{name: "push flag overrides", direction: "pull", push: true, want: "push"},
		{name: "both flags invalid", direction: "pull", pull: true, push: true, wantErr: true},
		{name: "invalid direction", direction: "sideways", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveSyncDirection(tt.direction, tt.pull, tt.push)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil and direction=%q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("unexpected direction: got=%q want=%q", got, tt.want)
			}
		})
	}
}

func TestPruneLocalClusterState(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "stackforge-cluster")
	if err := os.MkdirAll(filepath.Join(stateDir, "logs"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "inventory.yaml"), []byte("cluster_name: test\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := pruneLocalClusterState(stateDir); err != nil {
		t.Fatalf("unexpected prune error: %v", err)
	}
	if _, err := os.Stat(stateDir); !os.IsNotExist(err) {
		t.Fatalf("expected state dir to be removed, stat err=%v", err)
	}
}
