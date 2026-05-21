package upgrade

import (
	"fmt"
	"path/filepath"
	"time"

	"stackforge/internal/stackforge/backup"
	"stackforge/internal/stackforge/inventory"
)

type Options struct {
	StateDir   string
	Cluster    string
	DryRun     bool
	SkipBackup bool
}

func Run(opts Options) ([]string, error) {
	steps := []string{"check current versions", "validate target versions", "run pre-upgrade backup", "preserve previous configs", "upgrade stackforge-control-plane", "health check stackforge-control-plane", "upgrade traefik", "health check traefik", "upgrade consul", "health check consul", "upgrade nomad", "health check nomad", "print rollback path"}
	if opts.DryRun {
		return steps, nil
	}
	if !opts.SkipBackup {
		if _, err := backup.RunWithOptions(backup.Options{StateDir: opts.StateDir, Cluster: opts.Cluster}); err != nil {
			markUpgradeFailure(opts.StateDir, err)
			return nil, fmt.Errorf("pre-upgrade backup failed: %w", err)
		}
	} else {
		addInventoryWarning(opts.StateDir, "upgrade ran with --skip-backup; rollback may be incomplete")
	}
	err := fmt.Errorf("live upgrade requires explicit component target versions and reachable inventory; backup and refusal behavior are implemented, rollback path is latest backup under %s", filepath.Join(opts.StateDir, "backups"))
	markUpgradeFailure(opts.StateDir, err)
	return steps, err
}

func markUpgradeFailure(stateDir string, err error) {
	path := filepath.Join(stateDir, "inventory.yaml")
	inv, loadErr := inventory.Load(path)
	if loadErr != nil {
		return
	}
	inv.InstallStatus = "upgrade-failed"
	inv.FailedInstallStep = "upgrade"
	inv.Warnings = append(inv.Warnings, err.Error())
	inv.UpdatedAt = time.Now().UTC()
	_ = inventory.Save(path, inv)
}

func addInventoryWarning(stateDir, warning string) {
	path := filepath.Join(stateDir, "inventory.yaml")
	inv, err := inventory.Load(path)
	if err != nil {
		return
	}
	inv.Warnings = append(inv.Warnings, warning)
	_ = inventory.Save(path, inv)
}
