package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStatusClusterNoticeAutoDetected(t *testing.T) {
	orig := rootOpts
	t.Cleanup(func() { rootOpts = orig })
	rootOpts = orig
	rootOpts.output = "text"
	rootOpts.cluster = ""

	home := t.TempDir()
	t.Setenv("HOME", home)
	clusterDir := filepath.Join(home, ".stackforge", "stackforge-cluster")
	if err := os.MkdirAll(clusterDir, 0700); err != nil {
		t.Fatalf("mkdir cluster dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(clusterDir, "inventory.yaml"), []byte("cluster_name: stackforge-cluster\n"), 0600); err != nil {
		t.Fatalf("write inventory: %v", err)
	}

	note := statusClusterNotice()
	if !strings.Contains(note, "stackforge-cluster") {
		t.Fatalf("expected auto-detected cluster notice, got: %q", note)
	}
}

func TestStatusClusterNoticeExplicitClusterSuppressesHint(t *testing.T) {
	orig := rootOpts
	t.Cleanup(func() { rootOpts = orig })
	rootOpts = orig
	rootOpts.output = "text"
	rootOpts.cluster = "stackforge-production"

	if got := statusClusterNotice(); got != "" {
		t.Fatalf("expected empty notice for explicit cluster, got: %q", got)
	}
}

func TestStatusClusterNoticeJSONSuppressesHint(t *testing.T) {
	orig := rootOpts
	t.Cleanup(func() { rootOpts = orig })
	rootOpts = orig
	rootOpts.output = "json"
	rootOpts.cluster = ""

	if got := statusClusterNotice(); got != "" {
		t.Fatalf("expected empty notice for json output, got: %q", got)
	}
}

func TestStatusInventoryErrorNoLocalInventory(t *testing.T) {
	orig := rootOpts
	t.Cleanup(func() { rootOpts = orig })
	rootOpts = orig

	home := t.TempDir()
	t.Setenv("HOME", home)

	err := statusInventoryError(&os.PathError{Op: "open", Path: filepath.Join(home, ".stackforge", "stackforge-production", "inventory.yaml"), Err: os.ErrNotExist})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "no local inventory found") {
		t.Fatalf("expected missing local inventory hint, got: %v", err)
	}
}

func TestStatusInventoryErrorShowsAvailableClusters(t *testing.T) {
	orig := rootOpts
	t.Cleanup(func() { rootOpts = orig })
	rootOpts = orig
	rootOpts.cluster = "stackforge-production"

	home := t.TempDir()
	t.Setenv("HOME", home)
	clusterDir := filepath.Join(home, ".stackforge", "stackforge-cluster")
	if err := os.MkdirAll(clusterDir, 0700); err != nil {
		t.Fatalf("mkdir cluster dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(clusterDir, "inventory.yaml"), []byte("cluster_name: stackforge-cluster\n"), 0600); err != nil {
		t.Fatalf("write inventory: %v", err)
	}

	err := statusInventoryError(&os.PathError{Op: "open", Path: filepath.Join(home, ".stackforge", "stackforge-production", "inventory.yaml"), Err: os.ErrNotExist})
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "available local clusters: stackforge-cluster") {
		t.Fatalf("expected available clusters hint, got: %v", msg)
	}
	if !strings.Contains(msg, "use --cluster") {
		t.Fatalf("expected --cluster suggestion, got: %v", msg)
	}
}

func TestStatusInventoryErrorPassesThroughOtherErrors(t *testing.T) {
	orig := rootOpts
	t.Cleanup(func() { rootOpts = orig })
	rootOpts = orig

	boom := os.ErrPermission
	if got := statusInventoryError(boom); got != boom {
		t.Fatalf("expected passthrough error, got: %v", got)
	}
}
