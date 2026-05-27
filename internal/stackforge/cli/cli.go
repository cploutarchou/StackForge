package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	cpconfig "stackforge/internal/controlplane/config"
	cphttp "stackforge/internal/controlplane/http"
	sfbackup "stackforge/internal/stackforge/backup"
	"stackforge/internal/stackforge/config"
	"stackforge/internal/stackforge/install"
	"stackforge/internal/stackforge/inventory"
	"stackforge/internal/stackforge/rollback"
	"stackforge/internal/stackforge/safety"
	sfssh "stackforge/internal/stackforge/ssh"
	"stackforge/internal/stackforge/status"
	"stackforge/internal/stackforge/traefiklint"
	"stackforge/internal/stackforge/uninstall"
	"stackforge/internal/stackforge/upgrade"
	sfvalidate "stackforge/internal/stackforge/validate"
	sfverify "stackforge/internal/stackforge/verify"
)

var Version = "dev"

var rootOpts struct {
	configPath         string
	stateDir           string
	cluster            string
	output             string
	dryRun             bool
	yes                bool
	verbose            bool
	logLevel           string
	noColor            bool
	allowNoFirewall    bool
	allowExampleConfig bool
	allowPublicSSH     bool
	confirmProduction  bool
}

func Execute() error {
	return newRoot().Execute()
}

func newRoot() *cobra.Command {
	cmd := &cobra.Command{Use: "stackforge", Short: "StackForge infrastructure and domain control plane"}
	cmd.PersistentFlags().StringVar(&rootOpts.configPath, "config", "", "config file")
	cmd.PersistentFlags().StringVar(&rootOpts.stateDir, "state-dir", "", "state directory")
	cmd.PersistentFlags().StringVar(&rootOpts.cluster, "cluster", "", "cluster name")
	cmd.PersistentFlags().StringVar(&rootOpts.output, "output", "text", "output format: text|json")
	cmd.PersistentFlags().BoolVar(&rootOpts.dryRun, "dry-run", false, "show planned changes")
	cmd.PersistentFlags().BoolVar(&rootOpts.yes, "yes", false, "assume yes for dangerous confirmations")
	cmd.PersistentFlags().BoolVar(&rootOpts.verbose, "verbose", false, "verbose output")
	cmd.PersistentFlags().StringVar(&rootOpts.logLevel, "log-level", "info", "log level")
	cmd.PersistentFlags().BoolVar(&rootOpts.noColor, "no-color", false, "disable color")
	cmd.PersistentFlags().BoolVar(&rootOpts.allowNoFirewall, "allow-no-firewall", false, "allow install without firewall")
	cmd.PersistentFlags().BoolVar(&rootOpts.allowExampleConfig, "allow-example-config", false, "allow live commands to use example/demo config values")
	cmd.PersistentFlags().BoolVar(&rootOpts.allowPublicSSH, "allow-public-ssh", false, "allow allowed_ssh_cidrs to include 0.0.0.0/0")
	cmd.PersistentFlags().BoolVar(&rootOpts.confirmProduction, "confirm-production", false, "allow live actions against production environment after validation")
	_ = viper.BindPFlags(cmd.PersistentFlags())
	cmd.AddCommand(versionCmd(), installCmd(), statusCmd(), inventoryCmd(), nodesCmd(), componentsCmd(), firewallCmd(), domainsCmd(), consulCmd(), nomadCmd(), traefikCmd(), dbCmd(), backupCmd(), rollbackCmd(), validateCmd(), verifyCmd(), contextCmd(), upgradeCmd(), uninstallCmd(), serveCmd())
	return cmd
}

func versionCmd() *cobra.Command {
	return &cobra.Command{Use: "version", Short: "Print StackForge version", RunE: func(cmd *cobra.Command, args []string) error {
		if rootOpts.output == "json" {
			return output(map[string]string{"version": Version})
		}
		fmt.Println(Version)
		return nil
	}}
}

func installCmd() *cobra.Command {
	var resume bool
	var sshKey, sshUser string
	cmd := &cobra.Command{Use: "install", Short: "Install or resume StackForge infrastructure", RunE: func(cmd *cobra.Command, args []string) error {
		if rootOpts.configPath == "" {
			return fmt.Errorf("interactive wizard is not available in this non-TTY implementation; pass --config")
		}
		cfg, err := config.Load(rootOpts.configPath)
		if err != nil {
			return err
		}
		if sshKey != "" {
			cfg.SSH.PrivateKeyPath = sshKey
		}
		if sshUser != "" {
			cfg.SSH.User = sshUser
		}
		if rootOpts.cluster != "" {
			cfg.Cluster.Name = rootOpts.cluster
		}
		if !rootOpts.dryRun {
			safetyReport := safety.Check(cfg, safety.Options{Live: true, Production: strings.EqualFold(cfg.Cluster.Environment, "production"), ConfirmProduction: rootOpts.confirmProduction, AllowExampleConfig: rootOpts.allowExampleConfig, AllowPublicSSH: rootOpts.allowPublicSSH})
			for _, finding := range safetyReport.Findings {
				if finding.Severity == "warning" {
					fmt.Fprintln(os.Stderr, "WARNING:", finding.Message)
				}
			}
			if err := safetyReport.Error(); err != nil {
				return err
			}
			if err := confirmLiveInstall(cfg); err != nil {
				return err
			}
		}
		state := config.StateDir(rootOpts.stateDir, cfg.Cluster.Name)
		exec := sfssh.NewExecutor(cfg.SSH.User, cfg.SSH.Port, cfg.SSH.PrivateKeyPath)
		if rootOpts.dryRun {
			exec = nil
		}
		report, err := install.Run(context.Background(), install.Options{Config: cfg, StateDir: state, DryRun: rootOpts.dryRun, Resume: resume, AllowNoFirewall: rootOpts.allowNoFirewall, AllowExampleConfig: rootOpts.allowExampleConfig, AllowPublicSSH: rootOpts.allowPublicSSH, ConfirmProduction: rootOpts.confirmProduction, Executor: exec})
		if rootOpts.output == "json" {
			return printJSON(report, err)
		}
		if report != nil {
			for _, s := range report.Steps {
				prefix := map[install.Status]string{install.StatusOK: "[OK]", install.StatusSkipped: "[SKIP]", install.StatusFailed: "[FAIL]", install.StatusDryRun: "[DRY-RUN]"}[s.Status]
				if prefix == "" {
					prefix = "[WARN]"
				}
				fmt.Printf("%s %s %s\n", prefix, s.Node, s.Name)
				if s.Status == install.StatusDryRun && s.DryRunDescription != "" {
					for _, line := range strings.Split(s.DryRunDescription, "\n") {
						if strings.TrimSpace(line) != "" {
							fmt.Printf("  %s\n", line)
						}
					}
				}
			}
			fmt.Printf("[OK] inventory: %s\n[OK] secrets: %s\n[OK] report: %s\n", filepath.Join(state, "inventory.yaml"), filepath.Join(state, "generated-secrets.yaml"), filepath.Join(state, "install-report.json"))
		}
		if !rootOpts.dryRun {
			_ = refreshInventoryFromConfig(context.Background())
		}
		return err
	}}
	cmd.AddCommand(&cobra.Command{Use: "report", RunE: func(cmd *cobra.Command, args []string) error {
		report, err := install.LoadReport(filepath.Join(stateFromCluster(), "install-report.json"))
		if err != nil {
			return err
		}
		return output(report)
	}})
	cmd.AddCommand(&cobra.Command{Use: "resume-status", RunE: func(cmd *cobra.Command, args []string) error {
		report, err := install.LoadReport(filepath.Join(stateFromCluster(), "install-report.json"))
		if err != nil {
			return err
		}
		status := map[string]any{"cluster": report.Cluster, "failed_steps": report.FailedSteps, "suggested_recovery": report.SuggestedRecovery}
		return output(status)
	}})
	cmd.Flags().BoolVar(&resume, "resume", false, "resume from local install report")
	cmd.Flags().StringVar(&sshKey, "ssh-key", "", "SSH private key path")
	cmd.Flags().StringVar(&sshUser, "ssh-user", "", "SSH user")
	return cmd
}

func statusCmd() *cobra.Command {
	return &cobra.Command{Use: "status", Short: "Show StackForge cluster status", RunE: func(cmd *cobra.Command, args []string) error {
		inv, err := loadInventory()
		if err != nil {
			return err
		}
		if exec := executorFromConfig(); exec != nil && !rootOpts.dryRun {
			inventory.Refresh(context.Background(), inv, exec)
		} else {
			inventory.MarkHealthCheck(inv, "inventory-read", nil)
		}
		_ = inventory.Save(filepath.Join(stateFromCluster(), "inventory.yaml"), inv)
		return output(status.FromInventory(inv))
	}}
}

func inventoryCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "inventory", Short: "Harvest and show observed inventory"}
	cmd.AddCommand(&cobra.Command{Use: "show", RunE: func(cmd *cobra.Command, args []string) error {
		inv, err := loadInventory()
		if err != nil {
			return err
		}
		return output(inv)
	}})
	cmd.AddCommand(&cobra.Command{Use: "refresh", RunE: func(cmd *cobra.Command, args []string) error {
		inv, err := loadInventory()
		if err != nil {
			return err
		}
		warnings := inventory.Refresh(context.Background(), inv, executorFromConfig())
		if err := inventory.Save(filepath.Join(stateFromCluster(), "inventory.yaml"), inv); err != nil {
			return err
		}
		return output(map[string]any{"inventory": inv, "warnings": warnings})
	}})
	return cmd
}

func nodesCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "nodes", Short: "Manage nodes"}
	cmd.AddCommand(nodesBootstrapCmd(), nodesOnboardCmd())
	cmd.AddCommand(&cobra.Command{Use: "list", RunE: func(cmd *cobra.Command, args []string) error {
		inv, err := loadInventory()
		if err != nil {
			return err
		}
		return output(inv.Nodes)
	}})
	cmd.AddCommand(&cobra.Command{Use: "add", RunE: func(cmd *cobra.Command, args []string) error {
		if rootOpts.configPath == "" {
			return fmt.Errorf("nodes add requires --config so inventory can be reconciled with desired nodes")
		}
		cfg, err := config.Load(rootOpts.configPath)
		if err != nil {
			return err
		}
		inv, err := loadInventory()
		if err != nil {
			return err
		}
		existing := map[string]bool{}
		for _, n := range inv.Nodes {
			existing[n.Name] = true
		}
		added := []string{}
		for _, n := range cfg.Nodes {
			if existing[n.Name] {
				continue
			}
			inv.Nodes = append(inv.Nodes, inventory.Node{Name: n.Name, Roles: n.Roles, PrivateIP: n.Address, PublicIP: n.PublicAddress, SSH: inventory.SSHInfo{User: cfg.SSH.User, Port: cfg.SSH.Port, PrivateKeyPath: cfg.SSH.PrivateKeyPath}, Components: map[string]string{}, Services: map[string]string{}, HealthStatus: "pending-install"})
			added = append(added, n.Name)
		}
		inv.Warnings = append(inv.Warnings, "nodes add updated inventory only; run stackforge install --resume to perform live installation")
		inventory.Refresh(context.Background(), inv, executorFromConfig())
		if err := inventory.Save(filepath.Join(stateFromCluster(), "inventory.yaml"), inv); err != nil {
			return err
		}
		return output(map[string]any{"added": added, "next": "stackforge install --resume --config " + rootOpts.configPath})
	}})
	cmd.AddCommand(&cobra.Command{Use: "remove NODE", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		inv, err := loadInventory()
		if err != nil {
			return err
		}
		out := inv.Nodes[:0]
		removed := false
		for _, n := range inv.Nodes {
			if n.Name == args[0] {
				removed = true
				continue
			}
			out = append(out, n)
		}
		if !removed {
			return fmt.Errorf("node %s not found in inventory", args[0])
		}
		inv.Nodes = out
		inv.Warnings = append(inv.Warnings, "nodes remove updated inventory only; live Nomad/Consul drain and quorum changes require an explicit operator runbook")
		inventory.Refresh(context.Background(), inv, executorFromConfig())
		if err := inventory.Save(filepath.Join(stateFromCluster(), "inventory.yaml"), inv); err != nil {
			return err
		}
		return output(map[string]any{"removed": args[0], "live_changes": "not performed"})
	}})
	return cmd
}
func domainsCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "domains", Short: "Manage domains through the StackForge API"}
	cmd.AddCommand(domainsPoolCmd())
	var tenant, service string
	var port int
	add := &cobra.Command{Use: "add DOMAIN", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		body := map[string]any{"tenant_id": tenant, "domain": args[0], "target_service_name": service, "target_service_port": port}
		return apiRequest(http.MethodPost, "/api/v1/domains", body)
	}}
	add.Flags().StringVar(&tenant, "tenant", "", "tenant id")
	add.Flags().StringVar(&service, "service", "", "target service name")
	add.Flags().IntVar(&port, "port", 0, "target service port")
	cmd.AddCommand(add)
	cmd.AddCommand(&cobra.Command{Use: "list", RunE: func(cmd *cobra.Command, args []string) error {
		return apiRequest(http.MethodGet, "/api/v1/domains", nil)
	}})
	cmd.AddCommand(&cobra.Command{Use: "verify DOMAIN_OR_ID", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		return apiRequest(http.MethodPost, "/api/v1/domains/"+args[0]+"/verify", map[string]string{"token": os.Getenv("STACKFORGE_DOMAIN_VERIFICATION_TOKEN")})
	}})
	cmd.AddCommand(&cobra.Command{Use: "apply-dns DOMAIN_OR_ID", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		return apiRequest(http.MethodPost, "/api/v1/domains/"+args[0]+"/dns/apply", nil)
	}})
	cmd.AddCommand(&cobra.Command{Use: "apply-routing DOMAIN_OR_ID", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		return apiRequest(http.MethodPost, "/api/v1/domains/"+args[0]+"/routing/apply", nil)
	}})
	cmd.AddCommand(&cobra.Command{Use: "reconcile DOMAIN_OR_ID", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		return apiRequest(http.MethodPost, "/api/v1/domains/"+args[0]+"/reconcile", nil)
	}})
	cmd.AddCommand(&cobra.Command{Use: "disable DOMAIN_OR_ID", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		return apiRequest(http.MethodDelete, "/api/v1/domains/"+args[0]+"/routing", nil)
	}})
	cmd.AddCommand(&cobra.Command{Use: "delete DOMAIN_OR_ID", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		return apiRequest(http.MethodDelete, "/api/v1/domains/"+args[0], nil)
	}})
	return cmd
}

func consulCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "consul", Short: "Consul operations"}
	cmd.AddCommand(&cobra.Command{Use: "status", RunE: refuseLive("consul status")})
	cmd.AddCommand(&cobra.Command{Use: "members", RunE: refuseLive("consul members")})
	kv := &cobra.Command{Use: "kv"}
	kv.AddCommand(&cobra.Command{Use: "get KEY", Args: cobra.ExactArgs(1), RunE: refuseLive("consul kv get")})
	kv.AddCommand(&cobra.Command{Use: "put KEY VALUE", Args: cobra.ExactArgs(2), RunE: refuseLive("consul kv put")})
	snapshot := &cobra.Command{Use: "snapshot"}
	snapshot.AddCommand(&cobra.Command{Use: "save PATH", Args: cobra.ExactArgs(1), RunE: refuseLive("consul snapshot save")})
	snapshot.AddCommand(&cobra.Command{Use: "restore PATH", Args: cobra.ExactArgs(1), RunE: refuseLive("consul snapshot restore")})
	cmd.AddCommand(kv, snapshot)
	return cmd
}

func nomadCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "nomad", Short: "Nomad operations"}
	for _, n := range []string{"status", "nodes", "jobs", "allocations", "drain-node"} {
		cmd.AddCommand(&cobra.Command{Use: n, RunE: refuseLive("nomad " + n)})
	}
	return cmd
}

func traefikCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "traefik", Short: "Traefik operations"}
	for _, n := range []string{"status", "routes", "reload", "logs"} {
		cmd.AddCommand(&cobra.Command{Use: n, RunE: refuseLive("traefik " + n)})
	}
	cmd.AddCommand(&cobra.Command{Use: "lint-config PATH", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if err := traefiklint.LintFile(args[0]); err != nil {
			return err
		}
		if rootOpts.output == "json" {
			return output(map[string]any{"path": args[0], "valid": true})
		}
		fmt.Printf("[OK] %s\n", args[0])
		return nil
	}})
	return cmd
}

func dbCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "db", Short: "Database operations"}
	cmd.AddCommand(&cobra.Command{Use: "status", RunE: refuseLive("db status")})
	cmd.AddCommand(&cobra.Command{Use: "migrate", RunE: refuseLive("db migrate")})
	cmd.AddCommand(&cobra.Command{Use: "backup", RunE: func(cmd *cobra.Command, args []string) error { return runBackup() }})
	cmd.AddCommand(&cobra.Command{Use: "restore BACKUP_ID", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		report, err := sfbackup.RestoreWithDetailedOptions(sfbackup.RestoreOptions{StateDir: stateFromCluster(), BackupID: args[0], Yes: rootOpts.yes, DryRun: rootOpts.dryRun, Executor: executorFromConfig()})
		if rootOpts.output == "json" {
			return printJSON(report, err)
		}
		if report != nil {
			return output(report)
		}
		return err
	}})
	return cmd
}

func backupCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "backup", Short: "Backup and restore StackForge"}
	cmd.AddCommand(&cobra.Command{Use: "run", RunE: func(cmd *cobra.Command, args []string) error { return runBackup() }})
	cmd.AddCommand(&cobra.Command{Use: "list", RunE: func(cmd *cobra.Command, args []string) error {
		m, err := sfbackup.List(stateFromCluster())
		if err != nil {
			return err
		}
		return output(m)
	}})
	cmd.AddCommand(&cobra.Command{Use: "restore BACKUP_ID", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		report, err := sfbackup.RestoreWithDetailedOptions(sfbackup.RestoreOptions{StateDir: stateFromCluster(), BackupID: args[0], Yes: rootOpts.yes, DryRun: rootOpts.dryRun, Executor: executorFromConfig()})
		if report != nil {
			_ = output(report)
		}
		_ = refreshInventoryFromConfig(context.Background())
		return err
	}})
	return cmd
}

func rollbackCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "rollback", Short: "List or apply recorded rollback actions"}
	cmd.AddCommand(&cobra.Command{Use: "list", RunE: func(cmd *cobra.Command, args []string) error {
		records, err := rollback.List(stateFromCluster())
		if err != nil {
			return err
		}
		return output(records)
	}})
	cmd.AddCommand(&cobra.Command{Use: "apply ROLLBACK_ID", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		report, err := rollback.Apply(context.Background(), stateFromCluster(), args[0], rootOpts.yes, executorFromConfig())
		if report != nil {
			_ = output(report)
		}
		_ = refreshInventoryFromConfig(context.Background())
		return err
	}})
	return cmd
}

func validateCmd() *cobra.Command {
	var live, production bool
	cmd := &cobra.Command{Use: "validate", Short: "Preflight disposable nodes before install", RunE: func(cmd *cobra.Command, args []string) error {
		if rootOpts.configPath == "" {
			return fmt.Errorf("validate requires --config")
		}
		if _, err := os.Stat(rootOpts.configPath); err != nil {
			return err
		}
		cfg, err := config.Load(rootOpts.configPath)
		if err != nil {
			return err
		}
		var exec *sfssh.Executor
		if live {
			exec = executorFromConfig()
		}
		report := sfvalidate.RunWithOptions(context.Background(), cfg, exec, sfvalidate.Options{DryRun: rootOpts.dryRun || !live, Live: live, Production: production, AllowNoFirewall: rootOpts.allowNoFirewall, AllowExampleConfig: rootOpts.allowExampleConfig, AllowPublicSSH: rootOpts.allowPublicSSH, ConfirmProduction: true})
		if rootOpts.output == "json" {
			_ = printJSON(report, nil)
		} else {
			printValidationText(report)
		}
		if !report.Safe && !rootOpts.dryRun {
			return fmt.Errorf("validation failed")
		}
		return nil
	}}
	cmd.Flags().BoolVar(&live, "live", false, "run live SSH preflight checks")
	cmd.Flags().BoolVar(&production, "production", false, "validate production safety rules")
	return cmd
}

func verifyCmd() *cobra.Command {
	return &cobra.Command{Use: "verify", Short: "Verify observed live StackForge state", RunE: func(cmd *cobra.Command, args []string) error {
		inv, err := loadInventory()
		if err != nil {
			return err
		}
		report := sfverify.Run(context.Background(), stateFromCluster(), inv, executorForInventory(inv))
		if rootOpts.output == "json" {
			_ = printJSON(report, nil)
		} else {
			printVerifyText(report)
		}
		if !report.Safe {
			return fmt.Errorf("verify failed")
		}
		return nil
	}}
}

func upgradeCmd() *cobra.Command {
	var skip bool
	cmd := &cobra.Command{Use: "upgrade", RunE: func(cmd *cobra.Command, args []string) error {
		steps, err := upgrade.Run(upgrade.Options{StateDir: stateFromCluster(), Cluster: clusterName(), DryRun: rootOpts.dryRun, SkipBackup: skip})
		if rootOpts.output == "json" {
			return printJSON(steps, err)
		}
		for _, s := range steps {
			fmt.Println("[DRY-RUN]", s)
		}
		return err
	}}
	cmd.Flags().BoolVar(&skip, "skip-backup", false, "skip pre-upgrade backup")
	return cmd
}

func uninstallCmd() *cobra.Command {
	var confirm, wipe, preserve bool
	cmd := &cobra.Command{Use: "uninstall", RunE: func(cmd *cobra.Command, args []string) error {
		if preserve && wipe {
			return fmt.Errorf("--preserve-data and --wipe-data cannot both be set")
		}
		preserveData := true
		if wipe {
			preserveData = false
		}
		if preserve {
			preserveData = true
		}
		plan := uninstall.BuildPlan(preserveData)
		if !confirm {
			_ = output(plan)
			return fmt.Errorf("uninstall requires --confirm-destroy")
		}
		return uninstall.Run(stateFromCluster(), confirm, preserveData)
	}}
	cmd.Flags().BoolVar(&confirm, "confirm-destroy", false, "confirm destructive uninstall")
	cmd.Flags().BoolVar(&preserve, "preserve-data", false, "preserve remote and local data")
	cmd.Flags().BoolVar(&wipe, "wipe-data", false, "also wipe local data marker after explicit review")
	return cmd
}

func serveCmd() *cobra.Command {
	return &cobra.Command{Use: "serve", Hidden: false, Short: "Run StackForge control plane API", RunE: func(cmd *cobra.Command, args []string) error {
		cfg := cpconfig.FromEnv()
		if len(cfg.AdminAPIKeys) == 0 {
			return fmt.Errorf("STACKFORGE_ADMIN_API_KEYS is required")
		}
		server, err := cphttp.NewPersistent(context.Background(), cfg, nil)
		if err != nil {
			return err
		}
		return server.ListenAndServe()
	}}
}

func runBackup() error {
	m, err := sfbackup.RunWithOptions(sfbackup.Options{StateDir: stateFromCluster(), Cluster: clusterName(), DryRun: rootOpts.dryRun, Executor: executorFromConfig()})
	if err != nil {
		return err
	}
	_ = refreshInventoryFromConfig(context.Background())
	return output(m)
}

func confirmLiveInstall(cfg *config.Config) error {
	if rootOpts.yes {
		return nil
	}
	expected := "install " + cfg.Cluster.Name
	stat, err := os.Stdin.Stat()
	if err != nil || (stat.Mode()&os.ModeCharDevice) == 0 {
		return fmt.Errorf("live install requires --yes or interactive confirmation by typing %q", expected)
	}
	fmt.Fprintf(os.Stderr, "Live install will modify %d node(s) in cluster %s. Type %q to continue: ", len(cfg.Nodes), cfg.Cluster.Name, expected)
	text, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	if strings.TrimSpace(text) != expected {
		return fmt.Errorf("live install confirmation did not match %q", expected)
	}
	return nil
}

func loadInventory() (*inventory.Inventory, error) {
	return inventory.Load(filepath.Join(stateFromCluster(), "inventory.yaml"))
}

func stateFromCluster() string {
	return config.StateDir(rootOpts.stateDir, clusterName())
}

func clusterName() string {
	if rootOpts.cluster != "" {
		return rootOpts.cluster
	}
	if rootOpts.configPath != "" {
		if cfg, err := config.Load(rootOpts.configPath); err == nil {
			return cfg.Cluster.Name
		}
	}
	return "stackforge-production"
}

func executorFromConfig() *sfssh.Executor {
	if rootOpts.configPath == "" {
		return nil
	}
	cfg, err := config.Load(rootOpts.configPath)
	if err != nil {
		return nil
	}
	return sfssh.NewExecutor(cfg.SSH.User, cfg.SSH.Port, cfg.SSH.PrivateKeyPath)
}

func executorForInventory(inv *inventory.Inventory) *sfssh.Executor {
	if exec := executorFromConfig(); exec != nil {
		return exec
	}
	if inv == nil || len(inv.Nodes) == 0 {
		return nil
	}
	ssh := inv.Nodes[0].SSH
	if ssh.User == "" || ssh.PrivateKeyPath == "" {
		return nil
	}
	return sfssh.NewExecutor(ssh.User, ssh.Port, ssh.PrivateKeyPath)
}

func refreshInventoryFromConfig(ctx context.Context) error {
	inv, err := loadInventory()
	if err != nil {
		return err
	}
	inventory.Refresh(ctx, inv, executorFromConfig())
	return inventory.Save(filepath.Join(stateFromCluster(), "inventory.yaml"), inv)
}

func output(v any) error {
	if rootOpts.output == "json" {
		return printJSON(v, nil)
	}
	switch x := v.(type) {
	case []inventory.Node:
		for _, n := range x {
			fmt.Printf("%s\t%s\t%s\n", n.Name, n.PrivateIP, strings.Join(n.Roles, ","))
		}
	default:
		b, _ := json.MarshalIndent(v, "", "  ")
		fmt.Println(string(b))
	}
	return nil
}

func printValidationText(report sfvalidate.Report) {
	fmt.Printf("cluster: %s\nsafe: %t\n", report.Cluster, report.Safe)
	for _, check := range report.Checks {
		node := check.Node
		if node == "" {
			node = "local"
		}
		fmt.Printf("[%s] %s %s", strings.ToUpper(check.Status), node, check.Name)
		if check.Message != "" {
			fmt.Printf(": %s", check.Message)
		}
		fmt.Println()
	}
}

func printVerifyText(report sfverify.Report) {
	fmt.Printf("cluster: %s\nsafe: %t\n", report.Cluster, report.Safe)
	for _, check := range report.Checks {
		node := check.Node
		if node == "" {
			node = "local"
		}
		fmt.Printf("[%s] %s %s", strings.ToUpper(check.Status), node, check.Name)
		if check.Message != "" {
			fmt.Printf(": %s", check.Message)
		}
		fmt.Println()
	}
}

func printJSON(v any, prior error) error {
	b, _ := json.MarshalIndent(v, "", "  ")
	fmt.Fprintln(os.Stdout, string(b))
	return prior
}

func apiRequest(method, path string, body any) error {
	base := strings.TrimRight(os.Getenv("STACKFORGE_API_URL"), "/")
	if base == "" {
		base = "http://127.0.0.1:8080"
	}
	token := os.Getenv("STACKFORGE_ADMIN_API_KEY")
	if token == "" {
		token = os.Getenv("STACKFORGE_ADMIN_API_KEYS")
		if strings.Contains(token, ",") {
			token = strings.Split(token, ",")[0]
		}
	}
	if token == "" {
		return fmt.Errorf("STACKFORGE_ADMIN_API_KEY or STACKFORGE_ADMIN_API_KEYS is required")
	}
	var reader io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, base+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("API returned %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	fmt.Println(string(respBody))
	return nil
}

func refuseLive(name string) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		return fmt.Errorf("%s requires live component client configuration from inventory and secrets; refusing to fake production behavior", name)
	}
}
