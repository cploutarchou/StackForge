package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestClusterNamePrefersExplicitFlag(t *testing.T) {
	orig := rootOpts
	t.Cleanup(func() { rootOpts = orig })
	rootOpts = orig
	rootOpts.cluster = "explicit-cluster"

	if got := clusterName(); got != "explicit-cluster" {
		t.Fatalf("expected explicit cluster, got %q", got)
	}
}

func TestClusterNameFromConfig(t *testing.T) {
	orig := rootOpts
	t.Cleanup(func() { rootOpts = orig })
	rootOpts = orig

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "stackforge.yaml")
	cfg := "cluster:\n  name: from-config\n"
	if err := os.WriteFile(cfgPath, []byte(cfg), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	rootOpts.configPath = cfgPath

	if got := clusterName(); got != "from-config" {
		t.Fatalf("expected cluster from config, got %q", got)
	}
}

func TestClusterNameDiscoversSyncedCluster(t *testing.T) {
	orig := rootOpts
	t.Cleanup(func() { rootOpts = orig })
	rootOpts = orig

	home := t.TempDir()
	t.Setenv("HOME", home)
	clusterDir := filepath.Join(home, ".stackforge", "stackforge-cluster")
	if err := os.MkdirAll(clusterDir, 0700); err != nil {
		t.Fatalf("mkdir cluster dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(clusterDir, "inventory.yaml"), []byte("cluster_name: stackforge-cluster\n"), 0600); err != nil {
		t.Fatalf("write inventory: %v", err)
	}

	if got := clusterName(); got != "stackforge-cluster" {
		t.Fatalf("expected discovered cluster stackforge-cluster, got %q", got)
	}
}

func TestClusterNamePrefersProductionWhenInventoryExists(t *testing.T) {
	orig := rootOpts
	t.Cleanup(func() { rootOpts = orig })
	rootOpts = orig

	home := t.TempDir()
	t.Setenv("HOME", home)
	prodDir := filepath.Join(home, ".stackforge", "stackforge-production")
	clusterDir := filepath.Join(home, ".stackforge", "stackforge-cluster")
	if err := os.MkdirAll(prodDir, 0700); err != nil {
		t.Fatalf("mkdir prod dir: %v", err)
	}
	if err := os.MkdirAll(clusterDir, 0700); err != nil {
		t.Fatalf("mkdir cluster dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(prodDir, "inventory.yaml"), []byte("cluster_name: stackforge-production\n"), 0600); err != nil {
		t.Fatalf("write prod inventory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(clusterDir, "inventory.yaml"), []byte("cluster_name: stackforge-cluster\n"), 0600); err != nil {
		t.Fatalf("write cluster inventory: %v", err)
	}

	if got := clusterName(); got != "stackforge-production" {
		t.Fatalf("expected production cluster preference, got %q", got)
	}
}
