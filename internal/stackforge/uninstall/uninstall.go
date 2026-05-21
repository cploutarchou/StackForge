package uninstall

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"stackforge/internal/stackforge/inventory"
)

type Plan struct {
	RemoveServices []string `json:"remove_services"`
	RemovePackages []string `json:"remove_packages"`
	PreserveData   bool     `json:"preserve_data"`
}

func BuildPlan(preserveData bool) Plan {
	return Plan{RemoveServices: []string{"stackforge-control-plane.service", "stackforge-reconciler.service", "traefik", "nomad", "consul"}, RemovePackages: []string{"stackforge", "traefik", "nomad", "consul"}, PreserveData: preserveData}
}

func Run(stateDir string, confirm bool, preserveData bool) error {
	if !confirm {
		return fmt.Errorf("uninstall requires --confirm-destroy")
	}
	invPath := filepath.Join(stateDir, "inventory.yaml")
	if inv, err := inventory.Load(invPath); err == nil {
		inv.InstallStatus = "uninstalled"
		inv.UpdatedAt = time.Now().UTC()
		_ = inventory.Save(invPath, inv)
	}
	if !preserveData {
		return os.WriteFile(filepath.Join(stateDir, "UNINSTALL_REQUIRES_REMOTE_DATA_WIPE"), []byte("Remote data wipe must be performed only after reviewing the uninstall plan.\n"), 0600)
	}
	return nil
}
