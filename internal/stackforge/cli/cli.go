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
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	cpconfig "stackforge/internal/controlplane/config"
	cphttp "stackforge/internal/controlplane/http"
	sfbackup "stackforge/internal/stackforge/backup"
	"stackforge/internal/stackforge/config"
	"stackforge/internal/stackforge/install"
	"stackforge/internal/stackforge/inventory"
	"stackforge/internal/stackforge/remoteexec"
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
	cmd.AddCommand(versionCmd(), installCmd(), statusCmd(), inventoryCmd(), nodesCmd(), componentsCmd(), firewallCmd(), domainsCmd(), deployCmd(), consulCmd(), nomadCmd(), traefikCmd(), dbCmd(), backupCmd(), rollbackCmd(), validateCmd(), verifyCmd(), contextCmd(), upgradeCmd(), uninstallCmd(), serveCmd())
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
		if note := statusClusterNotice(); note != "" {
			fmt.Print(note)
		}
		inv, err := loadInventory()
		if err != nil {
			return statusInventoryError(err)
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

func statusClusterNotice() string {
	if rootOpts.output == "json" {
		return ""
	}
	if strings.TrimSpace(rootOpts.cluster) != "" {
		return ""
	}
	return fmt.Sprintf("[INFO] using cluster: %s (auto-detected)\n", clusterName())
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
	var address, datacenter string
	var stale bool
	cmd := &cobra.Command{Use: "consul", Short: "Consul operations"}
	cmd.PersistentFlags().StringVar(&address, "consul-http-addr", "", "Consul HTTP address override")
	cmd.PersistentFlags().StringVar(&datacenter, "dc", "", "Consul datacenter override")
	cmd.PersistentFlags().BoolVar(&stale, "stale", false, "allow stale Consul reads where supported")
	cmd.AddCommand(&cobra.Command{Use: "status", RunE: func(cmd *cobra.Command, args []string) error {
		return runConsulRead("status", "/v1/status/leader", address, datacenter, stale)
	}})
	cmd.AddCommand(&cobra.Command{Use: "members", RunE: func(cmd *cobra.Command, args []string) error {
		return runConsulRead("members", "/v1/agent/members", address, datacenter, stale)
	}})
	cmd.AddCommand(&cobra.Command{Use: "services", RunE: func(cmd *cobra.Command, args []string) error {
		return runConsulRead("services", "/v1/catalog/services", address, datacenter, stale)
	}})
	intentions := &cobra.Command{Use: "intentions", Short: "Consul intentions operations"}
	intentions.AddCommand(&cobra.Command{Use: "list", RunE: func(cmd *cobra.Command, args []string) error {
		return runConsulRead("intentions list", "/v1/connect/intentions", address, datacenter, stale)
	}})
	kv := &cobra.Command{Use: "kv"}
	kv.AddCommand(&cobra.Command{Use: "get KEY", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		return runConsulKVGet(args[0], address, datacenter, stale)
	}})
	kv.AddCommand(&cobra.Command{Use: "put KEY VALUE", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		return runConsulKVPut(args[0], args[1], address, datacenter)
	}})
	snapshot := &cobra.Command{Use: "snapshot"}
	snapshot.AddCommand(&cobra.Command{Use: "save PATH", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		return runConsulSnapshotSave(args[0], address, datacenter)
	}})
	snapshot.AddCommand(&cobra.Command{Use: "restore PATH", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		return runConsulSnapshotRestore(args[0], address, datacenter)
	}})
	cmd.AddCommand(kv, intentions, snapshot)
	return cmd
}

func nomadCmd() *cobra.Command {
	var namespace, region, datacenter, address string
	var drainDisable bool
	cmd := &cobra.Command{Use: "nomad", Short: "Nomad operations"}
	cmd.PersistentFlags().StringVar(&namespace, "namespace", "default", "Nomad namespace")
	cmd.PersistentFlags().StringVar(&region, "region", "", "Nomad region override")
	cmd.PersistentFlags().StringVar(&datacenter, "datacenter", "", "Nomad datacenter hint")
	cmd.PersistentFlags().StringVar(&address, "address", "", "Nomad HTTP address override")
	cmd.AddCommand(&cobra.Command{Use: "status", RunE: func(cmd *cobra.Command, args []string) error {
		return runNomadRead("status", "/v1/status/leader", namespace, region, datacenter, address)
	}})
	cmd.AddCommand(&cobra.Command{Use: "nodes", RunE: func(cmd *cobra.Command, args []string) error {
		return runNomadRead("nodes", "/v1/nodes", namespace, region, datacenter, address)
	}})
	cmd.AddCommand(&cobra.Command{Use: "jobs", RunE: func(cmd *cobra.Command, args []string) error {
		return runNomadRead("jobs", "/v1/jobs", namespace, region, datacenter, address)
	}})
	cmd.AddCommand(&cobra.Command{Use: "allocations", RunE: func(cmd *cobra.Command, args []string) error {
		return runNomadRead("allocations", "/v1/allocations", namespace, region, datacenter, address)
	}})
	alloc := &cobra.Command{Use: "alloc", Short: "Nomad allocation diagnostics"}
	var allocTask string
	allocStatus := &cobra.Command{Use: "status ALLOC_ID", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		return runNomadAllocStatus(args[0], namespace, region, datacenter, address)
	}}
	allocLogs := &cobra.Command{Use: "logs ALLOC_ID", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		return runNomadAllocLogs(args[0], allocTask, namespace, region, datacenter, address)
	}}
	allocLogs.Flags().StringVar(&allocTask, "task", "", "task name for allocation logs")
	alloc.AddCommand(allocStatus, allocLogs)
	cmd.AddCommand(alloc)
	job := &cobra.Command{Use: "job", Short: "Nomad job lifecycle operations"}
	job.AddCommand(&cobra.Command{Use: "plan FILE", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		return runNomadJobPlan(args[0], namespace, region, datacenter, address)
	}})
	job.AddCommand(&cobra.Command{Use: "run FILE", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		return runNomadJobRun(args[0], namespace, region, datacenter, address)
	}})
	job.AddCommand(&cobra.Command{Use: "stop JOB_ID", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		return runNomadJobStop(args[0], namespace, region, datacenter, address)
	}})
	cmd.AddCommand(job)
	drain := &cobra.Command{Use: "drain-node NODE_ID", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		return runNomadDrainNode(args[0], drainDisable, region, datacenter, address)
	}}
	drain.Flags().BoolVar(&drainDisable, "disable", false, "disable drain mode instead of enabling it")
	cmd.AddCommand(drain)
	return cmd
}

func runNomadRead(commandName, apiPath, namespace, region, datacenter, address string) error {
	inv, err := loadInventory()
	if err != nil {
		return err
	}
	target, err := selectNomadNode(inv)
	if err != nil {
		return err
	}
	nomadAddr := nomadAddressForRead(inv, strings.TrimSpace(address))
	warnings := []string{}
	if strings.TrimSpace(datacenter) != "" {
		warnings = append(warnings, "datacenter flag is recorded as context only for this read path")
	}
	result := map[string]any{
		"command":    "nomad " + commandName,
		"cluster":    clusterName(),
		"target":     target.Name,
		"dry_run":    rootOpts.dryRun,
		"status":     "ok",
		"result":     nil,
		"warnings":   warnings,
		"namespace":  strings.TrimSpace(namespace),
		"region":     strings.TrimSpace(region),
		"datacenter": strings.TrimSpace(datacenter),
		"address":    nomadAddr,
	}

	exec := executorForInventory(inv)
	if exec == nil || rootOpts.dryRun {
		result["status"] = "inventory-only"
		warnings = append(warnings, "no live executor available (or dry-run enabled); returning inventory snapshot")
		result["warnings"] = warnings
		result["result"] = map[string]any{
			"nomad_endpoints": inv.NomadEndpoints,
			"node_count":      len(inv.Nodes),
		}
		return output(result)
	}

	targetAddr := targetAddress(target)
	if strings.TrimSpace(targetAddr) == "" {
		return fmt.Errorf("node %s has no reachable address", target.Name)
	}

	remote := buildNomadReadRemoteCommand(nomadAddr, apiPath, strings.TrimSpace(namespace), strings.TrimSpace(region))
	runResult, runErr := exec.Run(context.Background(), targetAddr, remoteexec.Command{Command: remote, Sudo: true})
	if runErr != nil {
		result["status"] = "error"
		warnings = append(warnings, strings.TrimSpace(runResult.Stderr))
		result["warnings"] = warnings
		if strings.TrimSpace(runResult.Stdout) != "" {
			result["result"] = strings.TrimSpace(runResult.Stdout)
		}
		if rootOpts.output == "json" {
			_ = output(result)
		}
		return fmt.Errorf("nomad %s failed on %s: %w", commandName, target.Name, runErr)
	}

	decoded := decodeNomadPayload(runResult.Stdout)
	result["result"] = decoded
	result["warnings"] = warnings
	return output(result)
}

func selectNomadNode(inv *inventory.Inventory) (inventory.Node, error) {
	if inv == nil || len(inv.Nodes) == 0 {
		return inventory.Node{}, fmt.Errorf("inventory has no nodes")
	}
	for _, n := range inv.Nodes {
		if hasRole(n.Roles, "nomad-server") {
			return n, nil
		}
	}
	return inv.Nodes[0], nil
}

func nomadAddressForRead(inv *inventory.Inventory, override string) string {
	override = strings.TrimSpace(override)
	if override != "" {
		return override
	}
	if inv != nil && len(inv.NomadEndpoints) > 0 {
		if endpoint := strings.TrimSpace(inv.NomadEndpoints[0]); endpoint != "" {
			return endpoint
		}
	}
	return "http://127.0.0.1:4646"
}

func buildNomadReadRemoteCommand(address, apiPath, namespace, region string) string {
	parts := []string{}
	if token := nomadAuthToken(); strings.TrimSpace(token) != "" {
		parts = append(parts, "export NOMAD_TOKEN="+shellQuote(strings.TrimSpace(token)))
	}
	if namespace != "" {
		parts = append(parts, "export NOMAD_NAMESPACE="+shellQuote(namespace))
	}
	if region != "" {
		parts = append(parts, "export NOMAD_REGION="+shellQuote(region))
	}
	parts = append(parts, "curl -fsS "+shellQuote(strings.TrimRight(address, "/")+apiPath))
	return strings.Join(parts, " && ")
}

func decodeNomadPayload(raw string) any {
	text := strings.TrimSpace(raw)
	if text == "" {
		return map[string]any{}
	}
	var parsed any
	if err := json.Unmarshal([]byte(text), &parsed); err == nil {
		return parsed
	}
	return text
}

func runNomadJobPlan(filePath, namespace, region, datacenter, address string) error {
	b, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("read job file %s: %w", filePath, err)
	}
	_, target, targetAddr, nomadAddr, exec, err := resolveNomadExecution(address)
	if err != nil {
		return err
	}
	warnings := []string{}
	if strings.TrimSpace(datacenter) != "" {
		warnings = append(warnings, "datacenter flag is recorded as context only for this plan path")
	}
	result := map[string]any{
		"command":    "nomad job plan",
		"cluster":    clusterName(),
		"target":     target.Name,
		"dry_run":    rootOpts.dryRun,
		"status":     "ok",
		"result":     nil,
		"warnings":   warnings,
		"namespace":  strings.TrimSpace(namespace),
		"region":     strings.TrimSpace(region),
		"datacenter": strings.TrimSpace(datacenter),
		"address":    nomadAddr,
		"job_file":   filePath,
	}
	if exec == nil || rootOpts.dryRun {
		result["status"] = "inventory-only"
		warnings = append(warnings, "no live executor available (or dry-run enabled); plan was not executed remotely")
		result["warnings"] = warnings
		result["result"] = map[string]any{"planned": true}
		return output(result)
	}
	remotePath := nomadRemoteJobPath(filePath)
	if err := writeRemoteFile(context.Background(), exec, targetAddr, remotePath, b); err != nil {
		return err
	}
	remote := buildNomadJobRemoteCommand(nomadAddr, namespace, region, "plan", remotePath)
	runResult, runErr := exec.Run(context.Background(), targetAddr, remoteexec.Command{Command: remote, Sudo: true})
	if strings.TrimSpace(runResult.Stdout) != "" {
		result["result"] = strings.TrimSpace(runResult.Stdout)
	}
	if runErr != nil {
		result["status"] = "error"
		if strings.TrimSpace(runResult.Stderr) != "" {
			warnings = append(warnings, strings.TrimSpace(runResult.Stderr))
		}
		result["warnings"] = warnings
		if rootOpts.output == "json" {
			_ = output(result)
		}
		return fmt.Errorf("nomad job plan failed on %s: %w", target.Name, runErr)
	}
	result["warnings"] = warnings
	return output(result)
}

func runNomadJobRun(filePath, namespace, region, datacenter, address string) error {
	if rootOpts.dryRun {
		return runNomadJobPlan(filePath, namespace, region, datacenter, address)
	}
	b, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("read job file %s: %w", filePath, err)
	}
	inv, target, targetAddr, nomadAddr, exec, err := resolveNomadExecution(address)
	if err != nil {
		return err
	}
	if exec == nil {
		return fmt.Errorf("unable to resolve SSH executor; pass --config with valid ssh settings or ensure inventory has SSH credentials")
	}
	if err := enforceNomadLiveSafety(inv, "nomad job run "+filepath.Base(filePath)); err != nil {
		return err
	}
	warnings := []string{}
	if strings.TrimSpace(datacenter) != "" {
		warnings = append(warnings, "datacenter flag is recorded as context only for this run path")
	}
	result := map[string]any{
		"command":    "nomad job run",
		"cluster":    clusterName(),
		"target":     target.Name,
		"dry_run":    false,
		"status":     "ok",
		"result":     nil,
		"warnings":   warnings,
		"namespace":  strings.TrimSpace(namespace),
		"region":     strings.TrimSpace(region),
		"datacenter": strings.TrimSpace(datacenter),
		"address":    nomadAddr,
		"job_file":   filePath,
	}
	remotePath := nomadRemoteJobPath(filePath)
	if err := writeRemoteFile(context.Background(), exec, targetAddr, remotePath, b); err != nil {
		return err
	}
	remote := buildNomadJobRemoteCommand(nomadAddr, namespace, region, "run", remotePath)
	runResult, runErr := exec.Run(context.Background(), targetAddr, remoteexec.Command{Command: remote, Sudo: true})
	if strings.TrimSpace(runResult.Stdout) != "" {
		result["result"] = strings.TrimSpace(runResult.Stdout)
	}
	if runErr != nil {
		result["status"] = "error"
		if strings.TrimSpace(runResult.Stderr) != "" {
			warnings = append(warnings, strings.TrimSpace(runResult.Stderr))
		}
		result["warnings"] = warnings
		if rootOpts.output == "json" {
			_ = output(result)
		}
		return fmt.Errorf("nomad job run failed on %s: %w", target.Name, runErr)
	}
	result["warnings"] = warnings
	return output(result)
}

func runNomadJobStop(jobID, namespace, region, datacenter, address string) error {
	inv, target, targetAddr, nomadAddr, exec, err := resolveNomadExecution(address)
	if err != nil {
		return err
	}
	warnings := []string{}
	if strings.TrimSpace(datacenter) != "" {
		warnings = append(warnings, "datacenter flag is recorded as context only for this stop path")
	}
	result := map[string]any{
		"command":    "nomad job stop",
		"cluster":    clusterName(),
		"target":     target.Name,
		"dry_run":    rootOpts.dryRun,
		"status":     "ok",
		"result":     nil,
		"warnings":   warnings,
		"namespace":  strings.TrimSpace(namespace),
		"region":     strings.TrimSpace(region),
		"datacenter": strings.TrimSpace(datacenter),
		"address":    nomadAddr,
		"job_id":     strings.TrimSpace(jobID),
	}
	if rootOpts.dryRun {
		result["status"] = "planned"
		result["result"] = map[string]any{"would_stop": strings.TrimSpace(jobID)}
		return output(result)
	}
	if exec == nil {
		return fmt.Errorf("unable to resolve SSH executor; pass --config with valid ssh settings or ensure inventory has SSH credentials")
	}
	if err := enforceNomadLiveSafety(inv, "nomad job stop "+strings.TrimSpace(jobID)); err != nil {
		return err
	}
	remote := buildNomadJobStopRemoteCommand(nomadAddr, namespace, region, strings.TrimSpace(jobID))
	runResult, runErr := exec.Run(context.Background(), targetAddr, remoteexec.Command{Command: remote, Sudo: true})
	if strings.TrimSpace(runResult.Stdout) != "" {
		result["result"] = strings.TrimSpace(runResult.Stdout)
	}
	if runErr != nil {
		result["status"] = "error"
		if strings.TrimSpace(runResult.Stderr) != "" {
			warnings = append(warnings, strings.TrimSpace(runResult.Stderr))
		}
		result["warnings"] = warnings
		if rootOpts.output == "json" {
			_ = output(result)
		}
		return fmt.Errorf("nomad job stop failed on %s: %w", target.Name, runErr)
	}
	result["warnings"] = warnings
	return output(result)
}

func resolveNomadExecution(address string) (*inventory.Inventory, inventory.Node, string, string, *sfssh.Executor, error) {
	inv, err := loadInventory()
	if err != nil {
		return nil, inventory.Node{}, "", "", nil, err
	}
	target, err := selectNomadNode(inv)
	if err != nil {
		return nil, inventory.Node{}, "", "", nil, err
	}
	targetAddr := targetAddress(target)
	if strings.TrimSpace(targetAddr) == "" {
		return nil, inventory.Node{}, "", "", nil, fmt.Errorf("node %s has no reachable address", target.Name)
	}
	nomadAddr := nomadAddressForRead(inv, strings.TrimSpace(address))
	return inv, target, targetAddr, nomadAddr, executorForInventory(inv), nil
}

func enforceNomadLiveSafety(inv *inventory.Inventory, confirmationText string) error {
	if inv != nil && strings.EqualFold(strings.TrimSpace(inv.Environment), "production") && !rootOpts.confirmProduction {
		return fmt.Errorf("live nomad operation against production inventory requires --confirm-production")
	}
	if !rootOpts.yes {
		return confirmText(confirmationText)
	}
	return nil
}

func nomadRemoteJobPath(filePath string) string {
	base := sanitizeDeployName(filepath.Base(filePath))
	if base == "" {
		base = "job"
	}
	return "/tmp/stackforge-nomad-" + base + ".hcl"
}

func buildNomadJobRemoteCommand(address, namespace, region, mode, remotePath string) string {
	parts := []string{}
	if token := nomadAuthToken(); strings.TrimSpace(token) != "" {
		parts = append(parts, "export NOMAD_TOKEN="+shellQuote(strings.TrimSpace(token)))
	}
	if strings.TrimSpace(namespace) != "" {
		parts = append(parts, "export NOMAD_NAMESPACE="+shellQuote(strings.TrimSpace(namespace)))
	}
	if strings.TrimSpace(region) != "" {
		parts = append(parts, "export NOMAD_REGION="+shellQuote(strings.TrimSpace(region)))
	}
	parts = append(parts, "nomad job "+mode+" -address="+shellQuote(strings.TrimSpace(address))+" "+shellQuote(strings.TrimSpace(remotePath)))
	return strings.Join(parts, " && ")
}

func buildNomadJobStopRemoteCommand(address, namespace, region, jobID string) string {
	parts := []string{}
	if token := nomadAuthToken(); strings.TrimSpace(token) != "" {
		parts = append(parts, "export NOMAD_TOKEN="+shellQuote(strings.TrimSpace(token)))
	}
	if strings.TrimSpace(namespace) != "" {
		parts = append(parts, "export NOMAD_NAMESPACE="+shellQuote(strings.TrimSpace(namespace)))
	}
	if strings.TrimSpace(region) != "" {
		parts = append(parts, "export NOMAD_REGION="+shellQuote(strings.TrimSpace(region)))
	}
	parts = append(parts, "nomad job stop -yes -address="+shellQuote(strings.TrimSpace(address))+" "+shellQuote(strings.TrimSpace(jobID)))
	return strings.Join(parts, " && ")
}

func runNomadAllocStatus(allocID, namespace, region, datacenter, address string) error {
	_, target, targetAddr, nomadAddr, exec, err := resolveNomadExecution(address)
	if err != nil {
		return err
	}
	warnings := []string{}
	if strings.TrimSpace(datacenter) != "" {
		warnings = append(warnings, "datacenter flag is recorded as context only for this allocation status path")
	}
	result := map[string]any{
		"command":    "nomad alloc status",
		"cluster":    clusterName(),
		"target":     target.Name,
		"dry_run":    rootOpts.dryRun,
		"status":     "ok",
		"result":     nil,
		"warnings":   warnings,
		"namespace":  strings.TrimSpace(namespace),
		"region":     strings.TrimSpace(region),
		"datacenter": strings.TrimSpace(datacenter),
		"address":    nomadAddr,
		"alloc_id":   strings.TrimSpace(allocID),
	}
	if exec == nil || rootOpts.dryRun {
		result["status"] = "inventory-only"
		warnings = append(warnings, "no live executor available (or dry-run enabled); allocation status was not fetched")
		result["warnings"] = warnings
		return output(result)
	}
	remote := buildNomadAllocRemoteCommand(nomadAddr, namespace, region, "status", strings.TrimSpace(allocID), "")
	runResult, runErr := exec.Run(context.Background(), targetAddr, remoteexec.Command{Command: remote, Sudo: true})
	result["result"] = decodeNomadPayload(runResult.Stdout)
	if runErr != nil {
		result["status"] = "error"
		if strings.TrimSpace(runResult.Stderr) != "" {
			warnings = append(warnings, strings.TrimSpace(runResult.Stderr))
		}
		result["warnings"] = warnings
		if rootOpts.output == "json" {
			_ = output(result)
		}
		return fmt.Errorf("nomad alloc status failed on %s: %w", target.Name, runErr)
	}
	result["warnings"] = warnings
	return output(result)
}

func runNomadAllocLogs(allocID, task, namespace, region, datacenter, address string) error {
	_, target, targetAddr, nomadAddr, exec, err := resolveNomadExecution(address)
	if err != nil {
		return err
	}
	warnings := []string{}
	if strings.TrimSpace(datacenter) != "" {
		warnings = append(warnings, "datacenter flag is recorded as context only for this allocation logs path")
	}
	result := map[string]any{
		"command":    "nomad alloc logs",
		"cluster":    clusterName(),
		"target":     target.Name,
		"dry_run":    rootOpts.dryRun,
		"status":     "ok",
		"result":     nil,
		"warnings":   warnings,
		"namespace":  strings.TrimSpace(namespace),
		"region":     strings.TrimSpace(region),
		"datacenter": strings.TrimSpace(datacenter),
		"address":    nomadAddr,
		"alloc_id":   strings.TrimSpace(allocID),
		"task":       strings.TrimSpace(task),
	}
	if exec == nil || rootOpts.dryRun {
		result["status"] = "inventory-only"
		warnings = append(warnings, "no live executor available (or dry-run enabled); allocation logs were not fetched")
		result["warnings"] = warnings
		return output(result)
	}
	remote := buildNomadAllocRemoteCommand(nomadAddr, namespace, region, "logs", strings.TrimSpace(allocID), strings.TrimSpace(task))
	runResult, runErr := exec.Run(context.Background(), targetAddr, remoteexec.Command{Command: remote, Sudo: true})
	result["result"] = strings.TrimSpace(runResult.Stdout)
	if runErr != nil {
		result["status"] = "error"
		if strings.TrimSpace(runResult.Stderr) != "" {
			warnings = append(warnings, strings.TrimSpace(runResult.Stderr))
		}
		result["warnings"] = warnings
		if rootOpts.output == "json" {
			_ = output(result)
		}
		return fmt.Errorf("nomad alloc logs failed on %s: %w", target.Name, runErr)
	}
	result["warnings"] = warnings
	return output(result)
}

func buildNomadAllocRemoteCommand(address, namespace, region, mode, allocID, task string) string {
	parts := []string{}
	if token := nomadAuthToken(); strings.TrimSpace(token) != "" {
		parts = append(parts, "export NOMAD_TOKEN="+shellQuote(strings.TrimSpace(token)))
	}
	if strings.TrimSpace(namespace) != "" {
		parts = append(parts, "export NOMAD_NAMESPACE="+shellQuote(strings.TrimSpace(namespace)))
	}
	if strings.TrimSpace(region) != "" {
		parts = append(parts, "export NOMAD_REGION="+shellQuote(strings.TrimSpace(region)))
	}
	base := "nomad alloc " + mode + " -address=" + shellQuote(strings.TrimSpace(address))
	if strings.TrimSpace(mode) == "status" {
		base += " -json"
	}
	if strings.TrimSpace(mode) == "logs" && strings.TrimSpace(task) != "" {
		base += " -task " + shellQuote(strings.TrimSpace(task))
	}
	base += " " + shellQuote(strings.TrimSpace(allocID))
	parts = append(parts, base)
	return strings.Join(parts, " && ")
}

func runConsulRead(commandName, apiPath, address, datacenter string, stale bool) error {
	inv, target, targetAddr, consulAddr, exec, err := resolveConsulExecution(address)
	if err != nil {
		return err
	}
	query := []string{}
	if strings.TrimSpace(datacenter) != "" {
		query = append(query, "dc="+urlQueryEscape(strings.TrimSpace(datacenter)))
	}
	if stale {
		query = append(query, "stale")
	}
	endpoint := apiPath
	if len(query) > 0 {
		endpoint += "?" + strings.Join(query, "&")
	}
	warnings := []string{}
	result := map[string]any{
		"command":    "consul " + commandName,
		"cluster":    clusterName(),
		"target":     target.Name,
		"dry_run":    rootOpts.dryRun,
		"status":     "ok",
		"result":     nil,
		"warnings":   warnings,
		"address":    consulAddr,
		"datacenter": strings.TrimSpace(datacenter),
		"stale":      stale,
	}
	if exec == nil || rootOpts.dryRun {
		result["status"] = "inventory-only"
		warnings = append(warnings, "no live executor available (or dry-run enabled); returning inventory snapshot")
		result["warnings"] = warnings
		result["result"] = map[string]any{
			"consul_endpoints": inv.ConsulEndpoints,
			"node_count":       len(inv.Nodes),
		}
		return output(result)
	}
	remote, secrets := buildConsulCurlCommand(strings.TrimRight(consulAddr, "/")+endpoint, "GET", "")
	runResult, runErr := exec.Run(context.Background(), targetAddr, remoteexec.Command{Command: remote, Sudo: true, Secrets: secrets})
	result["result"] = decodeNomadPayload(runResult.Stdout)
	if runErr != nil {
		result["status"] = "error"
		if strings.TrimSpace(runResult.Stderr) != "" {
			warnings = append(warnings, strings.TrimSpace(runResult.Stderr))
		}
		result["warnings"] = warnings
		if rootOpts.output == "json" {
			_ = output(result)
		}
		return normalizeRemoteError("consul "+commandName, target.Name, runResult, runErr)
	}
	result["warnings"] = warnings
	return output(result)
}

func runConsulKVGet(key, address, datacenter string, stale bool) error {
	inv, target, targetAddr, consulAddr, exec, err := resolveConsulExecution(address)
	if err != nil {
		return err
	}
	query := []string{"raw"}
	if strings.TrimSpace(datacenter) != "" {
		query = append(query, "dc="+urlQueryEscape(strings.TrimSpace(datacenter)))
	}
	if stale {
		query = append(query, "stale")
	}
	endpoint := "/v1/kv/" + strings.ReplaceAll(strings.TrimSpace(key), " ", "%20") + "?" + strings.Join(query, "&")
	result := map[string]any{
		"command":    "consul kv get",
		"cluster":    clusterName(),
		"target":     target.Name,
		"dry_run":    rootOpts.dryRun,
		"status":     "ok",
		"result":     nil,
		"warnings":   []string{},
		"address":    consulAddr,
		"datacenter": strings.TrimSpace(datacenter),
		"stale":      stale,
		"key":        strings.TrimSpace(key),
	}
	if exec == nil || rootOpts.dryRun {
		result["status"] = "inventory-only"
		result["warnings"] = []string{"no live executor available (or dry-run enabled); returning inventory snapshot"}
		result["result"] = map[string]any{"consul_endpoints": inv.ConsulEndpoints, "node_count": len(inv.Nodes)}
		return output(result)
	}
	remote, secrets := buildConsulCurlCommand(strings.TrimRight(consulAddr, "/")+endpoint, "GET", "")
	runResult, runErr := exec.Run(context.Background(), targetAddr, remoteexec.Command{Command: remote, Sudo: true, Secrets: secrets})
	result["result"] = strings.TrimSpace(runResult.Stdout)
	if runErr != nil {
		result["status"] = "error"
		warnings := []string{}
		if strings.TrimSpace(runResult.Stderr) != "" {
			warnings = append(warnings, strings.TrimSpace(runResult.Stderr))
		}
		result["warnings"] = warnings
		if rootOpts.output == "json" {
			_ = output(result)
		}
		return normalizeRemoteError("consul kv get", target.Name, runResult, runErr)
	}
	return output(result)
}

func runConsulKVPut(key, value, address, datacenter string) error {
	inv, target, targetAddr, consulAddr, exec, err := resolveConsulExecution(address)
	if err != nil {
		return err
	}
	result := map[string]any{
		"command":    "consul kv put",
		"cluster":    clusterName(),
		"target":     target.Name,
		"dry_run":    rootOpts.dryRun,
		"status":     "ok",
		"result":     nil,
		"warnings":   []string{},
		"address":    consulAddr,
		"datacenter": strings.TrimSpace(datacenter),
		"key":        strings.TrimSpace(key),
	}
	if rootOpts.dryRun {
		result["status"] = "planned"
		result["result"] = map[string]any{"would_put": strings.TrimSpace(key)}
		return output(result)
	}
	if exec == nil {
		return fmt.Errorf("unable to resolve SSH executor; pass --config with valid ssh settings or ensure inventory has SSH credentials")
	}
	if err := enforceConsulLiveSafety(inv, "consul kv put "+strings.TrimSpace(key)); err != nil {
		return err
	}
	query := []string{}
	if strings.TrimSpace(datacenter) != "" {
		query = append(query, "dc="+urlQueryEscape(strings.TrimSpace(datacenter)))
	}
	endpoint := "/v1/kv/" + strings.ReplaceAll(strings.TrimSpace(key), " ", "%20")
	if len(query) > 0 {
		endpoint += "?" + strings.Join(query, "&")
	}
	remote, secrets := buildConsulCurlCommand(strings.TrimRight(consulAddr, "/")+endpoint, "PUT", value)
	secrets = append(secrets, value)
	runResult, runErr := exec.Run(context.Background(), targetAddr, remoteexec.Command{Command: remote, Sudo: true, Secrets: secrets})
	result["result"] = decodeNomadPayload(runResult.Stdout)
	if runErr != nil {
		result["status"] = "error"
		warnings := []string{}
		if strings.TrimSpace(runResult.Stderr) != "" {
			warnings = append(warnings, strings.TrimSpace(runResult.Stderr))
		}
		result["warnings"] = warnings
		if rootOpts.output == "json" {
			_ = output(result)
		}
		return normalizeRemoteError("consul kv put", target.Name, runResult, runErr)
	}
	return output(result)
}

func runConsulSnapshotSave(path, address, datacenter string) error {
	_, target, targetAddr, consulAddr, exec, err := resolveConsulExecution(address)
	if err != nil {
		return err
	}
	result := map[string]any{
		"command":    "consul snapshot save",
		"cluster":    clusterName(),
		"target":     target.Name,
		"dry_run":    rootOpts.dryRun,
		"status":     "ok",
		"result":     nil,
		"warnings":   []string{},
		"address":    consulAddr,
		"datacenter": strings.TrimSpace(datacenter),
		"path":       strings.TrimSpace(path),
	}
	if rootOpts.dryRun {
		result["status"] = "planned"
		result["result"] = map[string]any{"would_save_snapshot_to": strings.TrimSpace(path)}
		return output(result)
	}
	if exec == nil {
		return fmt.Errorf("unable to resolve SSH executor; pass --config with valid ssh settings or ensure inventory has SSH credentials")
	}
	cmd, secrets := buildConsulSnapshotCommand("save", strings.TrimSpace(path), consulAddr, strings.TrimSpace(datacenter))
	runResult, runErr := exec.Run(context.Background(), targetAddr, remoteexec.Command{Command: cmd, Sudo: true, Secrets: secrets})
	if strings.TrimSpace(runResult.Stdout) != "" {
		result["result"] = strings.TrimSpace(runResult.Stdout)
	}
	if runErr != nil {
		result["status"] = "error"
		if rootOpts.output == "json" {
			_ = output(result)
		}
		return normalizeRemoteError("consul snapshot save", target.Name, runResult, runErr)
	}
	return output(result)
}

func runConsulSnapshotRestore(path, address, datacenter string) error {
	inv, target, targetAddr, consulAddr, exec, err := resolveConsulExecution(address)
	if err != nil {
		return err
	}
	result := map[string]any{
		"command":    "consul snapshot restore",
		"cluster":    clusterName(),
		"target":     target.Name,
		"dry_run":    rootOpts.dryRun,
		"status":     "ok",
		"result":     nil,
		"warnings":   []string{},
		"address":    consulAddr,
		"datacenter": strings.TrimSpace(datacenter),
		"path":       strings.TrimSpace(path),
	}
	if rootOpts.dryRun {
		result["status"] = "planned"
		result["result"] = map[string]any{"would_restore_snapshot_from": strings.TrimSpace(path)}
		return output(result)
	}
	if exec == nil {
		return fmt.Errorf("unable to resolve SSH executor; pass --config with valid ssh settings or ensure inventory has SSH credentials")
	}
	if err := enforceConsulLiveSafety(inv, "consul snapshot restore "+strings.TrimSpace(path)); err != nil {
		return err
	}
	cmd, secrets := buildConsulSnapshotCommand("restore", strings.TrimSpace(path), consulAddr, strings.TrimSpace(datacenter))
	runResult, runErr := exec.Run(context.Background(), targetAddr, remoteexec.Command{Command: cmd, Sudo: true, Secrets: secrets})
	if strings.TrimSpace(runResult.Stdout) != "" {
		result["result"] = strings.TrimSpace(runResult.Stdout)
	}
	if runErr != nil {
		result["status"] = "error"
		if rootOpts.output == "json" {
			_ = output(result)
		}
		return normalizeRemoteError("consul snapshot restore", target.Name, runResult, runErr)
	}
	return output(result)
}

func runNomadDrainNode(nodeID string, disable bool, region, datacenter, address string) error {
	inv, target, targetAddr, nomadAddr, exec, err := resolveNomadExecution(address)
	if err != nil {
		return err
	}
	result := map[string]any{
		"command":    "nomad drain-node",
		"cluster":    clusterName(),
		"target":     target.Name,
		"dry_run":    rootOpts.dryRun,
		"status":     "ok",
		"result":     nil,
		"warnings":   []string{},
		"address":    nomadAddr,
		"region":     strings.TrimSpace(region),
		"datacenter": strings.TrimSpace(datacenter),
		"node_id":    strings.TrimSpace(nodeID),
		"disable":    disable,
	}
	if rootOpts.dryRun {
		result["status"] = "planned"
		result["result"] = map[string]any{"would_change_drain": strings.TrimSpace(nodeID), "disable": disable}
		return output(result)
	}
	if exec == nil {
		return fmt.Errorf("unable to resolve SSH executor; pass --config with valid ssh settings or ensure inventory has SSH credentials")
	}
	if err := enforceNomadLiveSafety(inv, "nomad drain-node "+strings.TrimSpace(nodeID)); err != nil {
		return err
	}
	cmd := buildNomadDrainRemoteCommand(nomadAddr, strings.TrimSpace(region), strings.TrimSpace(nodeID), disable)
	runResult, runErr := exec.Run(context.Background(), targetAddr, remoteexec.Command{Command: cmd, Sudo: true, Secrets: []string{nomadAuthToken()}})
	if strings.TrimSpace(runResult.Stdout) != "" {
		result["result"] = strings.TrimSpace(runResult.Stdout)
	}
	if runErr != nil {
		result["status"] = "error"
		if rootOpts.output == "json" {
			_ = output(result)
		}
		return normalizeRemoteError("nomad drain-node", target.Name, runResult, runErr)
	}
	return output(result)
}

func buildNomadDrainRemoteCommand(address, region, nodeID string, disable bool) string {
	parts := []string{}
	if token := nomadAuthToken(); strings.TrimSpace(token) != "" {
		parts = append(parts, "export NOMAD_TOKEN="+shellQuote(strings.TrimSpace(token)))
	}
	if strings.TrimSpace(region) != "" {
		parts = append(parts, "export NOMAD_REGION="+shellQuote(strings.TrimSpace(region)))
	}
	action := "-enable"
	if disable {
		action = "-disable"
	}
	parts = append(parts, "nomad node drain "+action+" -yes -address="+shellQuote(strings.TrimSpace(address))+" "+shellQuote(strings.TrimSpace(nodeID)))
	return strings.Join(parts, " && ")
}

func buildConsulCurlCommand(url, method, data string) (string, []string) {
	parts := []string{"curl -fsS"}
	secrets := []string{}
	if strings.TrimSpace(method) != "" && !strings.EqualFold(method, "GET") {
		parts = append(parts, "-X", shellQuote(strings.ToUpper(strings.TrimSpace(method))))
	}
	if token := consulAuthToken(); strings.TrimSpace(token) != "" {
		parts = append(parts, "-H", shellQuote("X-Consul-Token: "+strings.TrimSpace(token)))
		secrets = append(secrets, token)
	}
	if strings.TrimSpace(data) != "" {
		parts = append(parts, "-d", shellQuote(data))
	}
	parts = append(parts, shellQuote(strings.TrimSpace(url)))
	return strings.Join(parts, " "), secrets
}

func buildConsulSnapshotCommand(mode, path, address, datacenter string) (string, []string) {
	parts := []string{}
	secrets := []string{}
	if token := consulAuthToken(); strings.TrimSpace(token) != "" {
		parts = append(parts, "export CONSUL_HTTP_TOKEN="+shellQuote(strings.TrimSpace(token)))
		secrets = append(secrets, token)
	}
	cmd := "consul snapshot " + strings.TrimSpace(mode) + " -http-addr=" + shellQuote(strings.TrimSpace(address))
	if strings.TrimSpace(datacenter) != "" {
		cmd += " -datacenter=" + shellQuote(strings.TrimSpace(datacenter))
	}
	cmd += " " + shellQuote(strings.TrimSpace(path))
	parts = append(parts, cmd)
	return strings.Join(parts, " && "), secrets
}

func nomadAuthToken() string {
	if v := strings.TrimSpace(os.Getenv("NOMAD_TOKEN")); v != "" {
		return v
	}
	return strings.TrimSpace(os.Getenv("STACKFORGE_NOMAD_TOKEN"))
}

func consulAuthToken() string {
	if v := strings.TrimSpace(os.Getenv("CONSUL_HTTP_TOKEN")); v != "" {
		return v
	}
	return strings.TrimSpace(os.Getenv("STACKFORGE_CONSUL_HTTP_TOKEN"))
}

func normalizeRemoteError(action, target string, runResult remoteexec.Result, runErr error) error {
	if runErr == nil {
		return nil
	}
	message := strings.TrimSpace(runResult.Stderr)
	if message == "" {
		message = strings.TrimSpace(runResult.Stdout)
	}
	if message == "" {
		return fmt.Errorf("%s failed on %s: %w", action, target, runErr)
	}
	if len(message) > 240 {
		message = message[:240] + "..."
	}
	return fmt.Errorf("%s failed on %s: %s (%w)", action, target, message, runErr)
}

func resolveConsulExecution(address string) (*inventory.Inventory, inventory.Node, string, string, *sfssh.Executor, error) {
	inv, err := loadInventory()
	if err != nil {
		return nil, inventory.Node{}, "", "", nil, err
	}
	target, err := selectConsulNode(inv)
	if err != nil {
		return nil, inventory.Node{}, "", "", nil, err
	}
	targetAddr := targetAddress(target)
	if strings.TrimSpace(targetAddr) == "" {
		return nil, inventory.Node{}, "", "", nil, fmt.Errorf("node %s has no reachable address", target.Name)
	}
	consulAddr := consulAddressForRead(inv, strings.TrimSpace(address))
	return inv, target, targetAddr, consulAddr, executorForInventory(inv), nil
}

func selectConsulNode(inv *inventory.Inventory) (inventory.Node, error) {
	if inv == nil || len(inv.Nodes) == 0 {
		return inventory.Node{}, fmt.Errorf("inventory has no nodes")
	}
	for _, n := range inv.Nodes {
		if hasRole(n.Roles, "consul-server") {
			return n, nil
		}
	}
	return inv.Nodes[0], nil
}

func consulAddressForRead(inv *inventory.Inventory, override string) string {
	override = strings.TrimSpace(override)
	if override != "" {
		return override
	}
	if inv != nil && len(inv.ConsulEndpoints) > 0 {
		if endpoint := strings.TrimSpace(inv.ConsulEndpoints[0]); endpoint != "" {
			return endpoint
		}
	}
	return "http://127.0.0.1:8500"
}

func enforceConsulLiveSafety(inv *inventory.Inventory, confirmationText string) error {
	if inv != nil && strings.EqualFold(strings.TrimSpace(inv.Environment), "production") && !rootOpts.confirmProduction {
		return fmt.Errorf("live consul operation against production inventory requires --confirm-production")
	}
	if !rootOpts.yes {
		return confirmText(confirmationText)
	}
	return nil
}

func urlQueryEscape(s string) string {
	replacer := strings.NewReplacer(" ", "%20", "+", "%2B", "&", "%26", "=", "%3D")
	return replacer.Replace(s)
}

func traefikCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "traefik", Short: "Traefik operations"}
	for _, n := range []string{"status", "routes", "reload", "logs"} {
		cmd.AddCommand(&cobra.Command{Use: n, RunE: refuseLive("traefik " + n)})
	}
	consulCatalog := &cobra.Command{Use: "consul-catalog", Short: "Traefik Consul Catalog diagnostics"}
	consulCatalog.AddCommand(&cobra.Command{Use: "check", RunE: func(cmd *cobra.Command, args []string) error {
		return runTraefikConsulCatalogCheck()
	}})
	cmd.AddCommand(consulCatalog)
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

func runTraefikConsulCatalogCheck() error {
	inv, err := loadInventory()
	if err != nil {
		return err
	}
	var cfg *config.Config
	cfgLoaded := false
	if strings.TrimSpace(rootOpts.configPath) != "" {
		if c, cfgErr := config.Load(rootOpts.configPath); cfgErr == nil {
			cfg = c
			cfgLoaded = true
		}
	}
	findings := buildTraefikConsulCatalogFindings(inv, cfg, cfgLoaded)
	if exec := executorForInventory(inv); exec != nil && !rootOpts.dryRun {
		findings = append(findings, runTraefikConsulCatalogLiveChecks(inv, exec)...)
	}
	status := "ok"
	for _, finding := range findings {
		if finding["severity"] == "blocker" {
			status = "needs-attention"
			break
		}
	}
	result := map[string]any{
		"command":  "traefik consul-catalog check",
		"cluster":  clusterName(),
		"target":   "traefik+consul",
		"dry_run":  rootOpts.dryRun,
		"status":   status,
		"result":   map[string]any{"findings": findings},
		"warnings": []string{},
	}
	return output(result)
}

func runTraefikConsulCatalogLiveChecks(inv *inventory.Inventory, exec *sfssh.Executor) []map[string]string {
	findings := []map[string]string{}
	traefikNode, traefikErr := selectTraefikNode(inv)
	if traefikErr != nil {
		return append(findings, map[string]string{"severity": "warning", "code": "live.traefik_node_unavailable", "message": traefikErr.Error()})
	}
	if addr := targetAddress(traefikNode); strings.TrimSpace(addr) != "" {
		cmd := "curl -fsS http://127.0.0.1:8080/api/rawdata >/dev/null"
		if _, err := exec.Run(context.Background(), addr, remoteexec.Command{Command: cmd, Sudo: true}); err != nil {
			findings = append(findings, map[string]string{"severity": "warning", "code": "live.traefik_api_unreachable", "message": "unable to query Traefik rawdata API on selected traefik node"})
		} else {
			findings = append(findings, map[string]string{"severity": "info", "code": "live.traefik_api_ok", "message": "Traefik rawdata API responded on selected traefik node"})
		}
	}
	consulNode, consulErr := selectConsulNode(inv)
	if consulErr != nil {
		return append(findings, map[string]string{"severity": "warning", "code": "live.consul_node_unavailable", "message": consulErr.Error()})
	}
	if addr := targetAddress(consulNode); strings.TrimSpace(addr) != "" {
		cmd, secrets := buildConsulCurlCommand("http://127.0.0.1:8500/v1/catalog/services", "GET", "")
		if _, err := exec.Run(context.Background(), addr, remoteexec.Command{Command: cmd, Sudo: true, Secrets: secrets}); err != nil {
			findings = append(findings, map[string]string{"severity": "warning", "code": "live.consul_catalog_unreachable", "message": "unable to query Consul catalog services on selected consul node"})
		} else {
			findings = append(findings, map[string]string{"severity": "info", "code": "live.consul_catalog_ok", "message": "Consul catalog services endpoint responded on selected consul node"})
		}
	}
	return findings
}

func buildTraefikConsulCatalogFindings(inv *inventory.Inventory, cfg *config.Config, cfgLoaded bool) []map[string]string {
	findings := []map[string]string{}
	if inv == nil {
		return []map[string]string{{"severity": "blocker", "code": "inventory.missing", "message": "inventory is not available"}}
	}
	hasTraefikNode := false
	for _, n := range inv.Nodes {
		if hasRole(n.Roles, "traefik") {
			hasTraefikNode = true
			break
		}
	}
	if !hasTraefikNode {
		findings = append(findings, map[string]string{"severity": "blocker", "code": "traefik.node_missing", "message": "no node with role traefik found in inventory"})
	}
	if len(inv.ConsulEndpoints) == 0 {
		findings = append(findings, map[string]string{"severity": "blocker", "code": "consul.endpoint_missing", "message": "no consul endpoints discovered in inventory"})
	}
	if !cfgLoaded {
		findings = append(findings, map[string]string{"severity": "warning", "code": "config.not_loaded", "message": "--config not provided or unreadable; provider-specific checks are limited"})
		return findings
	}
	if cfg != nil {
		if cfg.Traefik.DashboardEnabled && !cfg.Traefik.DashboardBasicAuth {
			findings = append(findings, map[string]string{"severity": "warning", "code": "traefik.dashboard_auth", "message": "dashboard is enabled without basic auth"})
		}
		findings = append(findings, map[string]string{"severity": "info", "code": "provider.prefix", "message": "verify Traefik Consul Catalog provider prefix/tag conventions match your service tags"})
		findings = append(findings, map[string]string{"severity": "info", "code": "provider.refresh_interval", "message": "set a sane consul catalog refresh interval and throttle to avoid noisy reloads"})
		findings = append(findings, map[string]string{"severity": "info", "code": "provider.exposed_by_default", "message": "prefer exposedByDefault=false and explicit traefik.enable tags for least surprise"})
		findings = append(findings, map[string]string{"severity": "info", "code": "provider.connect_aware", "message": "enable connect-aware settings when using Consul Connect service mesh"})
	}
	return findings
}

func selectTraefikNode(inv *inventory.Inventory) (inventory.Node, error) {
	if inv == nil || len(inv.Nodes) == 0 {
		return inventory.Node{}, fmt.Errorf("inventory has no nodes")
	}
	for _, n := range inv.Nodes {
		if hasRole(n.Roles, "traefik") {
			return n, nil
		}
	}
	return inv.Nodes[0], nil
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
	if discovered := discoverLocalClusterName(rootOpts.stateDir); discovered != "" {
		return discovered
	}
	return "stackforge-production"
}

func statusInventoryError(err error) error {
	if !os.IsNotExist(err) {
		return err
	}
	base := config.StateDir(rootOpts.stateDir, "")
	clusters := discoverLocalClusters(base)
	path := filepath.Join(stateFromCluster(), "inventory.yaml")
	if len(clusters) == 0 {
		return fmt.Errorf("no local inventory found at %s; run context sync first or pass --cluster/--config", path)
	}
	return fmt.Errorf("inventory missing for cluster %q at %s; available local clusters: %s (use --cluster)", clusterName(), path, strings.Join(clusters, ", "))
}

func discoverLocalClusterName(stateDir string) string {
	base := config.StateDir(stateDir, "")
	if hasClusterInventory(base, "stackforge-production") {
		return "stackforge-production"
	}
	candidates := discoverLocalClusters(base)
	if len(candidates) == 0 {
		return ""
	}
	if len(candidates) == 1 {
		return candidates[0]
	}
	if hasValue(candidates, "stackforge-cluster") {
		return "stackforge-cluster"
	}
	sort.Strings(candidates)
	return candidates[0]
}

func discoverLocalClusters(base string) []string {
	entries, err := os.ReadDir(base)
	if err != nil {
		return nil
	}
	candidates := []string{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if hasClusterInventory(base, name) {
			candidates = append(candidates, name)
		}
	}
	sort.Strings(candidates)
	return candidates
}

func hasClusterInventory(base, cluster string) bool {
	if strings.TrimSpace(cluster) == "" {
		return false
	}
	path := filepath.Join(base, cluster, "inventory.yaml")
	stat, err := os.Stat(path)
	return err == nil && !stat.IsDir()
}

func hasValue(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
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
