package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"stackforge/internal/stackforge/config"
	"stackforge/internal/stackforge/safety"
)

func contextCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "context",
		Short: "Show the current StackForge operator context",
		RunE: func(cmd *cobra.Command, args []string) error {
			report := contextReport()
			if rootOpts.output == "json" {
				return output(report)
			}
			printContextText(report)
			return nil
		},
	}
}

func contextReport() map[string]any {
	report := map[string]any{
		"config":             rootOpts.configPath,
		"state_dir":          stateFromCluster(),
		"cluster":            clusterName(),
		"output":             rootOpts.output,
		"dry_run":            rootOpts.dryRun,
		"yes":                rootOpts.yes,
		"verbose":            rootOpts.verbose,
		"log_level":          rootOpts.logLevel,
		"no_color":           rootOpts.noColor,
		"allow_no_firewall":  rootOpts.allowNoFirewall,
		"allow_example_cfg":  rootOpts.allowExampleConfig,
		"allow_public_ssh":   rootOpts.allowPublicSSH,
		"confirm_production": rootOpts.confirmProduction,
	}
	if rootOpts.configPath == "" {
		report["config_loaded"] = false
		report["note"] = "pass --config to include cluster, DNS, SSH, and safety context"
		return report
	}
	cfg, err := config.Load(rootOpts.configPath)
	if err != nil {
		report["config_loaded"] = false
		report["config_error"] = err.Error()
		return report
	}
	report["config_loaded"] = true
	report["cluster_name"] = cfg.Cluster.Name
	report["cluster_environment"] = cfg.Cluster.Environment
	report["cluster_datacenter"] = cfg.Cluster.Datacenter
	report["node_count"] = len(cfg.Nodes)
	report["ssh"] = map[string]any{
		"user":     cfg.SSH.User,
		"port":     cfg.SSH.Port,
		"key_set":  strings.TrimSpace(cfg.SSH.PrivateKeyPath) != "",
		"copy_key": cfg.SSH.CopyPublicKey,
	}
	report["dns"] = map[string]any{
		"control_plane_domain": cfg.ControlPlane.Domain,
		"traefik_dashboard":    cfg.Traefik.DashboardDomain,
		"cloudflare_zone_set":  strings.TrimSpace(cfg.Cloudflare.DefaultZoneID) != "",
	}
	report["network"] = map[string]any{
		"allowed_admin_cidrs":  cfg.Network.AllowedAdminCIDRs,
		"allowed_ssh_cidrs":    cfg.Network.AllowedSSHCIDRs,
		"public_internal_comm": cfg.Network.AllowPublicInternalCommunication,
	}
	report["components"] = map[string]any{
		"consul":   map[string]any{"version": cfg.Consul.Version, "acl_enabled": cfg.Consul.ACLEnabled, "encrypt_gossip": cfg.Consul.EncryptGossip},
		"nomad":    map[string]any{"version": cfg.Nomad.Version, "acl_enabled": cfg.Nomad.ACLEnabled, "encrypt_gossip": cfg.Nomad.EncryptGossip},
		"traefik":  map[string]any{"version": cfg.Traefik.Version, "dashboard_enabled": cfg.Traefik.DashboardEnabled, "dashboard_basic_auth": cfg.Traefik.DashboardBasicAuth},
		"database": map[string]any{"engine": cfg.Database.Engine, "mode": cfg.Database.Mode, "backup_enabled": cfg.Database.BackupEnabled},
	}
	safetyReport := safety.Check(cfg, safety.Options{Live: true, Production: strings.EqualFold(cfg.Cluster.Environment, "production"), ConfirmProduction: rootOpts.confirmProduction, AllowExampleConfig: rootOpts.allowExampleConfig, AllowPublicSSH: rootOpts.allowPublicSSH})
	report["safety"] = map[string]any{
		"safe":       safetyReport.Safe,
		"findings":   safetyReport.Findings,
		"production": strings.EqualFold(cfg.Cluster.Environment, "production"),
	}
	return report
}

func printContextText(report map[string]any) {
	status := func(v bool) string {
		if v {
			return "yes"
		}
		return "no"
	}
	fmt.Printf("cluster: %s\n", stringValue(report["cluster"]))
	fmt.Printf("config: %s\n", stringValue(report["config"]))
	fmt.Printf("state_dir: %s\n", stringValue(report["state_dir"]))
	fmt.Printf("output: %s\n", stringValue(report["output"]))
	fmt.Printf("flags: dry_run=%s yes=%s verbose=%s log_level=%s no_color=%s\n", status(boolValue(report["dry_run"])), status(boolValue(report["yes"])), status(boolValue(report["verbose"])), stringValue(report["log_level"]), status(boolValue(report["no_color"])))
	fmt.Printf("safety_flags: allow_no_firewall=%s allow_example_cfg=%s allow_public_ssh=%s confirm_production=%s\n", status(boolValue(report["allow_no_firewall"])), status(boolValue(report["allow_example_cfg"])), status(boolValue(report["allow_public_ssh"])), status(boolValue(report["confirm_production"])))
	if loaded, _ := report["config_loaded"].(bool); !loaded {
		fmt.Printf("config_loaded: no\n")
		if msg := stringValue(report["config_error"]); msg != "" {
			fmt.Printf("config_error: %s\n", msg)
		}
		if msg := stringValue(report["note"]); msg != "" {
			fmt.Printf("note: %s\n", msg)
		}
		return
	}
	fmt.Printf("config_loaded: yes\n")
	fmt.Printf("cluster_name: %s\n", stringValue(report["cluster_name"]))
	fmt.Printf("cluster_environment: %s\n", stringValue(report["cluster_environment"]))
	fmt.Printf("cluster_datacenter: %s\n", stringValue(report["cluster_datacenter"]))
	fmt.Printf("node_count: %d\n", intValue(report["node_count"]))

	if ssh, ok := report["ssh"].(map[string]any); ok {
		fmt.Printf("ssh: user=%s port=%d key_set=%s copy_key=%s\n", stringValue(ssh["user"]), intValue(ssh["port"]), status(boolValue(ssh["key_set"])), status(boolValue(ssh["copy_key"])))
	}
	if dns, ok := report["dns"].(map[string]any); ok {
		fmt.Printf("dns: control_plane=%s traefik_dashboard=%s cloudflare_zone_set=%s\n", stringValue(dns["control_plane_domain"]), stringValue(dns["traefik_dashboard"]), status(boolValue(dns["cloudflare_zone_set"])))
	}
	if network, ok := report["network"].(map[string]any); ok {
		fmt.Printf("network: allowed_admin_cidrs=%s allowed_ssh_cidrs=%s public_internal_comm=%s\n", formatStrings(network["allowed_admin_cidrs"]), formatStrings(network["allowed_ssh_cidrs"]), status(boolValue(network["public_internal_comm"])))
	}
	if components, ok := report["components"].(map[string]any); ok {
		fmt.Printf("components:\n")
		printComponent := func(name string, value any) {
			if m, ok := value.(map[string]any); ok {
				fmt.Printf("  %s: %s\n", name, formatKeyValues(m))
			}
		}
		printComponent("consul", components["consul"])
		printComponent("nomad", components["nomad"])
		printComponent("traefik", components["traefik"])
		printComponent("database", components["database"])
	}
	if safetyReport, ok := report["safety"].(map[string]any); ok {
		fmt.Printf("safety: production=%s safe=%s\n", status(boolValue(safetyReport["production"])), status(boolValue(safetyReport["safe"])))
		switch findings := safetyReport["findings"].(type) {
		case []safety.Finding:
			if len(findings) > 0 {
				fmt.Printf("  findings:\n")
				for _, finding := range findings {
					fmt.Printf("  - %s %s: %s\n", finding.Severity, finding.Code, finding.Message)
				}
			}
		case []any:
			if len(findings) > 0 {
				fmt.Printf("  findings:\n")
				for _, finding := range findings {
					if m, ok := finding.(map[string]any); ok {
						fmt.Printf("  - %s %s: %s\n", stringValue(m["severity"]), stringValue(m["code"]), stringValue(m["message"]))
					}
				}
			}
		}
	}
}

func formatKeyValues(m map[string]any) string {
	parts := []string{}
	for _, key := range []string{"version", "engine", "mode", "backup_enabled", "acl_enabled", "encrypt_gossip", "dashboard_enabled", "dashboard_basic_auth"} {
		if v, ok := m[key]; ok {
			parts = append(parts, fmt.Sprintf("%s=%s", key, valueString(v)))
		}
	}
	return strings.Join(parts, " ")
}

func stringValue(v any) string {
	s, _ := v.(string)
	return s
}

func intValue(v any) int {
	switch x := v.(type) {
	case int:
		return x
	case int32:
		return int(x)
	case int64:
		return int(x)
	case float64:
		return int(x)
	default:
		return 0
	}
}

func boolValue(v any) bool {
	b, _ := v.(bool)
	return b
}

func formatStrings(v any) string {
	if xs, ok := v.([]string); ok {
		return strings.Join(xs, ",")
	}
	if xs, ok := v.([]any); ok {
		parts := make([]string, 0, len(xs))
		for _, x := range xs {
			parts = append(parts, valueString(x))
		}
		return strings.Join(parts, ",")
	}
	return valueString(v)
}

func valueString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case bool:
		if x {
			return "yes"
		}
		return "no"
	case int:
		return fmt.Sprintf("%d", x)
	case int64:
		return fmt.Sprintf("%d", x)
	case float64:
		return fmt.Sprintf("%d", int(x))
	default:
		return fmt.Sprintf("%v", x)
	}
}

func contextSyncCmd() *cobra.Command {
	var host, user, keyPath, remoteBase, direction string
	var prune, pullMode, pushMode bool
	var port int
	cmd := &cobra.Command{Use: "sync", Short: "Sync a cluster state directory between local and remote host", RunE: func(cmd *cobra.Command, args []string) error {
		if strings.TrimSpace(host) == "" {
			return fmt.Errorf("--host is required")
		}
		effectiveDirection, err := resolveSyncDirection(direction, pullMode, pushMode)
		if err != nil {
			return err
		}
		if prune && effectiveDirection != "pull" {
			return fmt.Errorf("--prune can only be used with pull direction")
		}
		stateDir := stateFromCluster()
		cluster := filepath.Base(stateDir)
		localBase := filepath.Dir(stateDir)
		target := sshTarget(user, host)
		if rootOpts.dryRun {
			return output(map[string]any{
				"dry_run":         true,
				"operation":       "context sync",
				"direction":       effectiveDirection,
				"prune":           prune,
				"cluster":         cluster,
				"local_state_dir": stateDir,
				"remote_base":     remoteBase,
				"remote_target":   target,
			})
		}
		if err := os.MkdirAll(localBase, 0700); err != nil {
			return err
		}
		sshArgs := sshBaseArgs(port, keyPath)
		if err := ensureRemoteBaseDir(context.Background(), target, remoteBase, sshArgs); err != nil {
			return err
		}
		if prune && effectiveDirection == "pull" {
			if err := pruneLocalClusterState(stateDir); err != nil {
				return err
			}
		}
		if err := syncStateDirectory(context.Background(), effectiveDirection, localBase, cluster, target, remoteBase, sshArgs); err != nil {
			return err
		}
		if err := hardenLocalStatePermissions(stateDir); err != nil {
			return err
		}
		return output(map[string]any{
			"operation":       "context sync",
			"direction":       effectiveDirection,
			"prune":           prune,
			"cluster":         cluster,
			"local_state_dir": stateDir,
			"remote_base":     remoteBase,
			"remote_target":   target,
			"status":          "ok",
		})
	}}
	cmd.Flags().StringVar(&host, "host", "", "remote host/IP used for sync")
	cmd.Flags().StringVar(&user, "user", "root", "remote SSH user")
	cmd.Flags().IntVar(&port, "port", 22, "remote SSH port")
	cmd.Flags().StringVar(&keyPath, "ssh-key", "", "SSH private key path used for sync")
	cmd.Flags().StringVar(&remoteBase, "remote-base", "~/.stackforge", "remote StackForge state base directory")
	cmd.Flags().StringVar(&direction, "direction", "pull", "sync direction: pull|push")
	cmd.Flags().BoolVar(&pullMode, "pull", false, "shortcut for --direction pull")
	cmd.Flags().BoolVar(&pushMode, "push", false, "shortcut for --direction push")
	cmd.Flags().BoolVar(&prune, "prune", false, "when pulling, remove local cluster state directory before extraction")
	return cmd
}

func resolveSyncDirection(direction string, pullMode, pushMode bool) (string, error) {
	if pullMode && pushMode {
		return "", fmt.Errorf("--pull and --push cannot be used together")
	}
	if pullMode {
		return "pull", nil
	}
	if pushMode {
		return "push", nil
	}
	resolved := strings.ToLower(strings.TrimSpace(direction))
	if resolved != "push" && resolved != "pull" {
		return "", fmt.Errorf("--direction must be pull or push")
	}
	return resolved, nil
}

func pruneLocalClusterState(stateDir string) error {
	if strings.TrimSpace(stateDir) == "" {
		return fmt.Errorf("state directory is empty")
	}
	return os.RemoveAll(stateDir)
}

func contextExecCmd() *cobra.Command {
	var host, user, keyPath, remoteBin, remoteStateDir string
	var port int
	cmd := &cobra.Command{Use: "exec --host HOST -- [STACKFORGE_ARGS...]", Short: "Execute a StackForge CLI command remotely over SSH", Args: cobra.ArbitraryArgs, RunE: func(cmd *cobra.Command, args []string) error {
		if strings.TrimSpace(host) == "" {
			return fmt.Errorf("--host is required")
		}
		if len(args) == 0 {
			return fmt.Errorf("provide remote stackforge arguments after --, e.g. stackforge context exec --host 10.0.0.10 -- verify --confirm-production")
		}
		target := sshTarget(user, host)
		remoteArgs := buildRemoteStackforgeArgs(args, clusterName(), remoteStateDir)
		if rootOpts.dryRun {
			return output(map[string]any{
				"dry_run":        true,
				"operation":      "context exec",
				"remote_target":  target,
				"remote_command": append([]string{remoteBin}, remoteArgs...),
			})
		}
		sshArgs := append(sshBaseArgs(port, keyPath), target)
		sshArgs = append(sshArgs, remoteBin)
		sshArgs = append(sshArgs, remoteArgs...)
		run := exec.CommandContext(context.Background(), "ssh", sshArgs...)
		run.Stdin = os.Stdin
		run.Stdout = os.Stdout
		run.Stderr = os.Stderr
		return run.Run()
	}}
	cmd.Flags().StringVar(&host, "host", "", "remote host/IP used for command execution")
	cmd.Flags().StringVar(&user, "user", "root", "remote SSH user")
	cmd.Flags().IntVar(&port, "port", 22, "remote SSH port")
	cmd.Flags().StringVar(&keyPath, "ssh-key", "", "SSH private key path for remote execution")
	cmd.Flags().StringVar(&remoteBin, "remote-bin", "stackforge", "remote StackForge executable")
	cmd.Flags().StringVar(&remoteStateDir, "remote-state-dir", "~/.stackforge", "remote StackForge state base directory")
	return cmd
}

func syncStateDirectory(ctx context.Context, direction, localBase, cluster, target, remoteBase string, sshArgs []string) error {
	switch direction {
	case "push":
		producer := exec.CommandContext(ctx, "tar", "-C", localBase, "-czf", "-", cluster)
		consumerArgs := append([]string{}, sshArgs...)
		consumerArgs = append(consumerArgs, target, "tar", "-xzf", "-", "-C", remoteBase)
		consumer := exec.CommandContext(ctx, "ssh", consumerArgs...)
		if err := pipeCommands(producer, consumer); err != nil {
			return fmt.Errorf("context sync push failed: %w", err)
		}
		return nil
	case "pull":
		producerArgs := append([]string{}, sshArgs...)
		producerArgs = append(producerArgs, target, "tar", "-C", remoteBase, "-czf", "-", cluster)
		producer := exec.CommandContext(ctx, "ssh", producerArgs...)
		consumer := exec.CommandContext(ctx, "tar", "-xzf", "-", "-C", localBase)
		if err := pipeCommands(producer, consumer); err != nil {
			return fmt.Errorf("context sync pull failed: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("unsupported direction %q", direction)
	}
}

func ensureRemoteBaseDir(ctx context.Context, target, remoteBase string, sshArgs []string) error {
	args := append([]string{}, sshArgs...)
	args = append(args, target, "mkdir", "-p", remoteBase)
	run := exec.CommandContext(ctx, "ssh", args...)
	if out, err := run.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to create remote base dir %q: %w: %s", remoteBase, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func pipeCommands(producer, consumer *exec.Cmd) error {
	stdoutPipe, err := producer.StdoutPipe()
	if err != nil {
		return err
	}
	var producerErr bytes.Buffer
	var consumerErr bytes.Buffer
	producer.Stderr = &producerErr
	consumer.Stderr = &consumerErr
	consumer.Stdin = stdoutPipe

	if err := consumer.Start(); err != nil {
		return fmt.Errorf("start consumer: %w", err)
	}
	if err := producer.Start(); err != nil {
		_ = consumer.Process.Kill()
		_, _ = consumer.Process.Wait()
		return fmt.Errorf("start producer: %w", err)
	}

	producerWaitErr := producer.Wait()
	consumerWaitErr := consumer.Wait()
	if producerWaitErr != nil || consumerWaitErr != nil {
		return fmt.Errorf("producer error=%v stderr=%q; consumer error=%v stderr=%q", producerWaitErr, strings.TrimSpace(producerErr.String()), consumerWaitErr, strings.TrimSpace(consumerErr.String()))
	}
	return nil
}

func buildRemoteStackforgeArgs(args []string, cluster, remoteStateDir string) []string {
	trimmed := make([]string, 0, len(args))
	for _, arg := range args {
		if strings.TrimSpace(arg) != "" {
			trimmed = append(trimmed, arg)
		}
	}
	if len(trimmed) > 0 && trimmed[0] == "stackforge" {
		trimmed = trimmed[1:]
	}
	out := append([]string{}, trimmed...)
	if !hasFlag(out, "--cluster") && strings.TrimSpace(cluster) != "" {
		out = append([]string{"--cluster", cluster}, out...)
	}
	if !hasFlag(out, "--state-dir") && strings.TrimSpace(remoteStateDir) != "" {
		out = append([]string{"--state-dir", remoteStateDir}, out...)
	}
	if rootOpts.output == "json" && !hasFlag(out, "--output") {
		out = append([]string{"--output", "json"}, out...)
	}
	if rootOpts.confirmProduction && !hasFlag(out, "--confirm-production") {
		out = append([]string{"--confirm-production"}, out...)
	}
	if rootOpts.allowExampleConfig && !hasFlag(out, "--allow-example-config") {
		out = append([]string{"--allow-example-config"}, out...)
	}
	if rootOpts.allowNoFirewall && !hasFlag(out, "--allow-no-firewall") {
		out = append([]string{"--allow-no-firewall"}, out...)
	}
	if rootOpts.allowPublicSSH && !hasFlag(out, "--allow-public-ssh") {
		out = append([]string{"--allow-public-ssh"}, out...)
	}
	if rootOpts.verbose && !hasFlag(out, "--verbose") {
		out = append([]string{"--verbose"}, out...)
	}
	if rootOpts.logLevel != "" && rootOpts.logLevel != "info" && !hasFlag(out, "--log-level") {
		out = append([]string{"--log-level", rootOpts.logLevel}, out...)
	}
	return out
}

func hasFlag(args []string, flag string) bool {
	for i, arg := range args {
		if arg == flag {
			return true
		}
		if strings.HasPrefix(arg, flag+"=") {
			return true
		}
		if i > 0 && strings.HasPrefix(args[i-1], "-") && !strings.HasPrefix(arg, "-") {
			continue
		}
	}
	return false
}

func sshTarget(user, host string) string {
	if strings.TrimSpace(user) == "" {
		return host
	}
	return user + "@" + host
}

func sshBaseArgs(port int, keyPath string) []string {
	args := []string{"-o", "BatchMode=yes", "-p", strconv.Itoa(port)}
	if strings.TrimSpace(keyPath) != "" {
		args = append(args, "-i", expandHomePath(keyPath))
	}
	return args
}

func hardenLocalStatePermissions(stateDir string) error {
	if err := os.Chmod(stateDir, 0700); err != nil && !os.IsNotExist(err) {
		return err
	}
	secret := filepath.Join(stateDir, "generated-secrets.yaml")
	if err := os.Chmod(secret, 0600); err != nil && !os.IsNotExist(err) {
		return err
	}
	inv := filepath.Join(stateDir, "inventory.yaml")
	if err := os.Chmod(inv, 0600); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func expandHomePath(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, _ := os.UserHomeDir()
		if home != "" {
			return filepath.Join(home, strings.TrimPrefix(path, "~/"))
		}
	}
	return path
}
