package cli

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/crypto/ssh/terminal"

	"stackforge/internal/controlplane/dns/cloudflare"
	"stackforge/internal/stackforge/bootstrap"
	"stackforge/internal/stackforge/components"
	"stackforge/internal/stackforge/config"
	"stackforge/internal/stackforge/domainpool"
	"stackforge/internal/stackforge/firewall"
	"stackforge/internal/stackforge/install"
	"stackforge/internal/stackforge/inventory"
	"stackforge/internal/stackforge/remoteexec"
	sfssh "stackforge/internal/stackforge/ssh"
	sfverify "stackforge/internal/stackforge/verify"
)

func nodesBootstrapCmd() *cobra.Command {
	var nodeSpecs []string
	var sshUser, auth, publicKey string
	var sshPort int
	cmd := &cobra.Command{Use: "bootstrap", Short: "Bootstrap passwordless SSH access for nodes", RunE: func(cmd *cobra.Command, args []string) error {
		nodes, err := bootstrapNodesFromFlags(nodeSpecs, sshUser, sshPort, auth, publicKey)
		if err != nil {
			return err
		}
		if len(nodes) == 0 {
			nodes, err = promptBootstrapNodes()
			if err != nil {
				return err
			}
		}
		if !rootOpts.dryRun && !rootOpts.yes {
			printBootstrapPlan(nodes)
			if err := confirmText("bootstrap " + clusterName()); err != nil {
				return err
			}
		}
		report, err := bootstrap.Run(context.Background(), bootstrap.Options{ClusterName: clusterName(), Environment: "production", StateDir: stateFromCluster(), Nodes: nodes, DryRun: rootOpts.dryRun, PasswordReader: securePasswordPrompt})
		if report != nil && rootOpts.output != "json" {
			for _, step := range report.Steps {
				fmt.Printf("[%s] %s %s", strings.ToUpper(step.Status), step.Node, step.Name)
				if step.Message != "" {
					fmt.Printf(": %s", step.Message)
				}
				fmt.Println()
			}
			fmt.Printf("report: %s\n", report.ReportPath)
		}
		if rootOpts.output == "json" {
			return printJSON(report, err)
		}
		return err
	}}
	cmd.Flags().StringArrayVar(&nodeSpecs, "node", nil, "node in name=public-ip-or-hostname form; repeatable")
	cmd.Flags().StringVar(&sshUser, "ssh-user", "root", "SSH username")
	cmd.Flags().IntVar(&sshPort, "ssh-port", 22, "SSH port")
	cmd.Flags().StringVar(&publicKey, "public-key", "~/.ssh/id_ed25519.pub", "local public SSH key path")
	cmd.Flags().StringVar(&auth, "auth", bootstrap.AuthPrivateKey, "auth method: password|private-key")
	return cmd
}

func nodesOnboardCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "onboard", Short: "Interactively onboard servers and install StackForge components", RunE: func(cmd *cobra.Command, args []string) error {
		cfg, bootNodes, options, err := onboardPlanFromInput()
		if err != nil {
			return err
		}
		printOnboardPlan(cfg, options)
		if rootOpts.dryRun {
			return runOnboard(cfg, bootNodes, options)
		}
		if !rootOpts.yes {
			if err := confirmText("onboard " + cfg.Cluster.Name); err != nil {
				return err
			}
		}
		return runOnboard(cfg, bootNodes, options)
	}}
	return cmd
}

type onboardOptions struct {
	CopySSHKey        bool
	InstallDocker     bool
	ApplyFirewall     bool
	InstallComponents bool
}

func onboardPlanFromInput() (*config.Config, []bootstrap.Node, onboardOptions, error) {
	if rootOpts.configPath != "" {
		cfg, err := config.Load(rootOpts.configPath)
		if err != nil {
			return nil, nil, onboardOptions{}, err
		}
		nodes := bootstrapNodesFromConfig(cfg, bootstrap.AuthPrivateKey, "~/.ssh/id_ed25519.pub")
		return cfg, nodes, onboardOptions{CopySSHKey: cfg.SSH.CopyPublicKey, InstallDocker: true, ApplyFirewall: !rootOpts.allowNoFirewall, InstallComponents: true}, nil
	}
	if !isTerminal() {
		return nil, nil, onboardOptions{}, fmt.Errorf("nodes onboard requires --config or an interactive TTY")
	}
	reader := bufio.NewReader(os.Stdin)
	cluster := ask(reader, "Cluster name", "stackforge-staging")
	env := ask(reader, "Environment", "staging")
	adminCIDRs := splitList(ask(reader, "Allowed admin CIDRs", "127.0.0.1/32"))
	sshCIDRs := splitList(ask(reader, "Allowed SSH CIDRs", "127.0.0.1/32"))
	controlDomain := ask(reader, "Control-plane domain", "control.example.com")
	count, err := askInt(reader, "How many servers do you want to add?", 1)
	if err != nil {
		return nil, nil, onboardOptions{}, err
	}
	var nodes []config.NodeConfig
	var boot []bootstrap.Node
	for i := 0; i < count; i++ {
		fmt.Printf("Server %d\n", i+1)
		name := ask(reader, "Server name", fmt.Sprintf("node-%d", i+1))
		public := ask(reader, "Public IP or hostname", "")
		private := ask(reader, "Private IP if available", public)
		user := ask(reader, "SSH username", "root")
		port, err := askInt(reader, "SSH port", 22)
		if err != nil {
			return nil, nil, onboardOptions{}, err
		}
		auth := ask(reader, "Authentication method (password/private-key)", bootstrap.AuthPrivateKey)
		roles := splitList(ask(reader, "Roles (comma separated)", "consul-server,nomad-server,nomad-client,traefik,database,control-plane,docker-host"))
		pubKey := ask(reader, "Local public SSH key path", "~/.ssh/id_ed25519.pub")
		nodes = append(nodes, config.NodeConfig{Name: name, Address: private, PublicAddress: public, Roles: roles})
		boot = append(boot, bootstrap.Node{Name: name, Address: public, PrivateIP: private, User: user, Port: port, Auth: auth, PublicKeyPath: pubKey, Roles: roles})
	}
	copyKey := askBool(reader, "Should StackForge copy your local public SSH key?", true)
	installDocker := askBool(reader, "Install Docker when missing?", true)
	applyFirewall := askBool(reader, "Apply firewall rules?", true)
	installComponents := askBool(reader, "Install required StackForge components immediately?", true)
	cfg := &config.Config{
		Cluster:      config.ClusterConfig{Name: cluster, Environment: env, Datacenter: "dc1"},
		SSH:          config.SSHConfig{User: boot[0].User, Port: boot[0].Port, PrivateKeyPath: bootstrap.PrivateKeyPathForPublic(boot[0].PublicKeyPath), CopyPublicKey: copyKey},
		Nodes:        nodes,
		Network:      config.NetworkConfig{AllowedAdminCIDRs: adminCIDRs, AllowedSSHCIDRs: sshCIDRs},
		Database:     config.DatabaseConfig{Engine: "postgres"},
		ControlPlane: config.ControlPlaneConfig{Domain: controlDomain, APIPort: 8080, AdminAPIKeys: []string{"generated-by-install"}},
		Traefik:      config.TraefikConfig{DashboardEnabled: false},
	}
	return cfg, boot, onboardOptions{CopySSHKey: copyKey, InstallDocker: installDocker, ApplyFirewall: applyFirewall, InstallComponents: installComponents}, nil
}

func runOnboard(cfg *config.Config, bootNodes []bootstrap.Node, opts onboardOptions) error {
	state := config.StateDir(rootOpts.stateDir, cfg.Cluster.Name)
	if opts.CopySSHKey {
		report, err := bootstrap.Run(context.Background(), bootstrap.Options{ClusterName: cfg.Cluster.Name, Environment: cfg.Cluster.Environment, StateDir: state, Nodes: bootNodes, DryRun: rootOpts.dryRun, PasswordReader: securePasswordPrompt})
		if report != nil && rootOpts.output != "json" {
			fmt.Printf("bootstrap report: %s\n", report.ReportPath)
		}
		if err != nil {
			return err
		}
	}
	if opts.InstallDocker {
		inv := inventoryFromConfig(cfg)
		if !rootOpts.dryRun {
			if existing, err := inventory.Load(filepath.Join(state, "inventory.yaml")); err == nil {
				inv = existing
			}
		}
		exec := executorForConfig(cfg)
		items, err := components.RunInstall(context.Background(), inv, exec, components.Docker, "", rootOpts.dryRun)
		if err != nil && !rootOpts.dryRun {
			return err
		}
		for _, item := range items {
			if rootOpts.output != "json" {
				fmt.Printf("[%s] %s install %s\n", strings.ToUpper(valueOr(item.Status, "planned")), item.Node, item.Component)
			}
		}
	}
	if !opts.ApplyFirewall {
		rootOpts.allowNoFirewall = true
	}
	if opts.InstallComponents {
		exec := executorForConfig(cfg)
		if rootOpts.dryRun {
			exec = nil
		}
		report, err := install.Run(context.Background(), install.Options{Config: cfg, StateDir: state, DryRun: rootOpts.dryRun, AllowNoFirewall: !opts.ApplyFirewall || rootOpts.allowNoFirewall, AllowExampleConfig: rootOpts.allowExampleConfig, AllowPublicSSH: rootOpts.allowPublicSSH, ConfirmProduction: rootOpts.confirmProduction, Executor: exec})
		if report != nil && rootOpts.output != "json" {
			fmt.Printf("install report: %s\n", filepath.Join(state, "install-report.json"))
		}
		if err != nil {
			return err
		}
	}
	if !rootOpts.dryRun && opts.InstallComponents {
		inv, err := inventory.Load(filepath.Join(state, "inventory.yaml"))
		if err == nil {
			verification := sfverify.Run(context.Background(), state, inv, executorForConfig(cfg))
			printVerifyText(verification)
			if !verification.Safe {
				return fmt.Errorf("post-onboarding verification failed")
			}
		}
	}
	return nil
}

func componentsCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "components", Short: "Install and inspect node components"}
	var node string
	installCmd := &cobra.Command{Use: "install COMPONENT", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		inv, err := inventoryForComponentCommand()
		if err != nil {
			return err
		}
		if !rootOpts.dryRun && !rootOpts.yes {
			if err := confirmText("install component " + args[0]); err != nil {
				return err
			}
		}
		items, err := components.RunInstall(context.Background(), inv, executorForInventory(inv), args[0], node, rootOpts.dryRun)
		if rootOpts.output == "json" {
			return printJSON(items, err)
		}
		for _, item := range items {
			fmt.Printf("[%s] %s %s\n", strings.ToUpper(valueOr(item.Status, "planned")), item.Node, item.Component)
			if rootOpts.dryRun {
				fmt.Printf("  %s\n", item.Command)
			}
			if item.Error != "" {
				fmt.Printf("  error: %s\n", item.Error)
			}
		}
		return err
	}}
	cmd.AddCommand(installCmd)
	cmd.AddCommand(&cobra.Command{Use: "status", RunE: func(cmd *cobra.Command, args []string) error {
		inv, err := inventoryForComponentCommand()
		if err != nil {
			return err
		}
		var statuses []components.Status
		exec := executorForInventory(inv)
		for _, n := range inv.Nodes {
			if node != "" && n.Name != node {
				continue
			}
			addr := n.PrivateIP
			if addr == "" {
				addr = n.PublicIP
			}
			if exec != nil && !rootOpts.dryRun {
				res, err := exec.Run(context.Background(), addr, remoteexec.Command{Command: components.StatusCommand(), Sudo: true, Timeout: 45 * time.Second})
				if err != nil {
					statuses = append(statuses, components.Status{Node: n.Name, Component: "ssh", Warnings: []string{err.Error()}})
					continue
				}
				statuses = append(statuses, components.ParseStatus(n.Name, res.Stdout)...)
			} else {
				for component, version := range n.Versions {
					statuses = append(statuses, components.Status{Node: n.Name, Component: component, Installed: version != "", Version: version, Systemd: n.Services[component], Ports: n.Listening})
				}
			}
		}
		return output(statuses)
	}})
	cmd.PersistentFlags().StringVar(&node, "node", "", "target node name")
	return cmd
}

func firewallCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "firewall", Short: "Plan and apply firewall rules"}
	cmd.AddCommand(&cobra.Command{Use: "plan", RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load(rootOpts.configPath)
		if err != nil {
			return err
		}
		plan, err := firewall.BuildPlanWithOptions(cfg, firewall.Options{AllowPublicSSH: rootOpts.allowPublicSSH})
		if err != nil {
			return err
		}
		if rootOpts.output == "json" {
			return output(plan)
		}
		for _, r := range plan.Rules {
			fmt.Printf("%s\t%s\t%d/%s\tfrom %s\texposure=%s\treason=%s\n", r.Node, r.Role, r.Port, r.Protocol, r.Source, r.Exposure, r.Purpose)
		}
		return nil
	}})
	cmd.AddCommand(&cobra.Command{Use: "apply", RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load(rootOpts.configPath)
		if err != nil {
			return err
		}
		plan, err := firewall.BuildPlanWithOptions(cfg, firewall.Options{AllowPublicSSH: rootOpts.allowPublicSSH})
		if err != nil {
			return err
		}
		for _, r := range plan.Rules {
			fmt.Printf("%s\t%s\t%d/%s\tfrom %s\texposure=%s\treason=%s\n", r.Node, r.Role, r.Port, r.Protocol, r.Source, r.Exposure, r.Purpose)
		}
		if rootOpts.dryRun {
			return nil
		}
		if !rootOpts.yes {
			if err := confirmText("apply firewall " + cfg.Cluster.Name); err != nil {
				return err
			}
		}
		exec := executorForConfig(cfg)
		if exec == nil {
			return fmt.Errorf("remote executor is required")
		}
		command := firewall.BackupCommand() + " && " + strings.Join(firewall.UFWCommands(plan), " && ")
		for _, n := range cfg.Nodes {
			if _, err := exec.Run(context.Background(), n.Address, remoteexec.Command{Command: command, Sudo: true, Timeout: 3 * time.Minute}); err != nil {
				return err
			}
		}
		return nil
	}})
	return cmd
}

func domainsPoolCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "pool", Short: "Manage the StackForge domain pool"}
	var target, targetValue, recordType, zoneID string
	var proxied, allowInternal, allowWildcard bool
	add := &cobra.Command{Use: "add DOMAIN", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		e, err := domainpool.Add(domainPoolPath(), domainpool.Entry{Domain: args[0], TargetType: target, TargetValue: targetValue, RecordType: recordType, ZoneID: zoneID, Proxied: proxied}, allowInternal, allowWildcard)
		return printJSONOrOutput(e, err)
	}}
	add.Flags().StringVar(&target, "target", "traefik", "target type: traefik|control-plane|custom")
	add.Flags().StringVar(&targetValue, "target-value", "", "DNS record target value")
	add.Flags().StringVar(&recordType, "record-type", "A", "record type: A|CNAME")
	add.Flags().StringVar(&zoneID, "zone-id", "", "Cloudflare zone id")
	add.Flags().BoolVar(&proxied, "proxied", false, "enable Cloudflare proxying")
	add.Flags().BoolVar(&allowInternal, "allow-internal", false, "allow internal/private targets")
	add.Flags().BoolVar(&allowWildcard, "allow-wildcard", false, "allow wildcard domains")
	cmd.AddCommand(add)
	cmd.AddCommand(&cobra.Command{Use: "list", RunE: func(cmd *cobra.Command, args []string) error {
		s, err := domainpool.Load(domainPoolPath())
		if err != nil {
			return err
		}
		return output(s.Entries)
	}})
	cmd.AddCommand(&cobra.Command{Use: "remove DOMAIN", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if !rootOpts.yes && !rootOpts.dryRun {
			if err := confirmText("remove domain " + args[0]); err != nil {
				return err
			}
		}
		if rootOpts.dryRun {
			return output(map[string]string{"dry_run": "would disable " + args[0] + " in domain pool"})
		}
		e, err := domainpool.Remove(domainPoolPath(), args[0])
		return printJSONOrOutput(e, err)
	}})
	cmd.AddCommand(&cobra.Command{Use: "apply-dns DOMAIN", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if !rootOpts.yes && !rootOpts.dryRun {
			if err := confirmText("apply dns " + args[0]); err != nil {
				return err
			}
		}
		e, err := domainpool.ApplyDNS(context.Background(), args[0], domainpool.ApplyOptions{Path: domainPoolPath(), AuditPath: domainPoolAuditPath(), DryRun: rootOpts.dryRun, Client: &cloudflare.Client{Token: os.Getenv("CLOUDFLARE_API_TOKEN")}})
		return printJSONOrOutput(e, err)
	}})
	cmd.AddCommand(&cobra.Command{Use: "verify-dns DOMAIN", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		e, err := domainpool.VerifyDNS(context.Background(), domainPoolPath(), args[0], nil)
		return printJSONOrOutput(e, err)
	}})
	return cmd
}

func bootstrapNodesFromFlags(specs []string, user string, port int, auth string, publicKey string) ([]bootstrap.Node, error) {
	var nodes []bootstrap.Node
	for _, spec := range specs {
		name, addr, ok := strings.Cut(spec, "=")
		if !ok || strings.TrimSpace(name) == "" || strings.TrimSpace(addr) == "" {
			return nil, fmt.Errorf("--node must use name=public-ip-or-hostname")
		}
		nodes = append(nodes, bootstrap.Node{Name: strings.TrimSpace(name), Address: strings.TrimSpace(addr), User: user, Port: port, Auth: auth, PublicKeyPath: publicKey})
	}
	return nodes, nil
}

func bootstrapNodesFromConfig(cfg *config.Config, auth, publicKey string) []bootstrap.Node {
	var nodes []bootstrap.Node
	for _, n := range cfg.Nodes {
		addr := n.PublicAddress
		if addr == "" {
			addr = n.Address
		}
		nodes = append(nodes, bootstrap.Node{Name: n.Name, Address: addr, PrivateIP: n.Address, User: cfg.SSH.User, Port: cfg.SSH.Port, Auth: auth, PublicKeyPath: publicKey, PrivateKeyPath: cfg.SSH.PrivateKeyPath, Roles: n.Roles})
	}
	return nodes
}

func promptBootstrapNodes() ([]bootstrap.Node, error) {
	if !isTerminal() {
		return nil, fmt.Errorf("nodes bootstrap requires --node flags or an interactive TTY")
	}
	reader := bufio.NewReader(os.Stdin)
	count, err := askInt(reader, "How many servers do you want to bootstrap?", 1)
	if err != nil {
		return nil, err
	}
	var nodes []bootstrap.Node
	for i := 0; i < count; i++ {
		fmt.Printf("Server %d\n", i+1)
		name := ask(reader, "Server name", fmt.Sprintf("node-%d", i+1))
		addr := ask(reader, "Public IP or hostname", "")
		user := ask(reader, "SSH username", "root")
		port, err := askInt(reader, "SSH port", 22)
		if err != nil {
			return nil, err
		}
		auth := ask(reader, "Authentication method (password/private-key)", bootstrap.AuthPassword)
		pubKey := ask(reader, "Local public SSH key path", "~/.ssh/id_ed25519.pub")
		nodes = append(nodes, bootstrap.Node{Name: name, Address: addr, User: user, Port: port, Auth: auth, PublicKeyPath: pubKey})
	}
	return nodes, nil
}

func securePasswordPrompt(node bootstrap.Node) (string, error) {
	fmt.Fprintf(os.Stderr, "Password auth is used only for initial bootstrap and will not be stored.\nSSH password for %s@%s:%d: ", node.User, node.Address, node.Port)
	b, err := terminal.ReadPassword(int(syscall.Stdin))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	if len(b) == 0 {
		return "", fmt.Errorf("empty password refused")
	}
	return string(b), nil
}

func ask(r *bufio.Reader, label, def string) string {
	if def != "" {
		fmt.Fprintf(os.Stderr, "%s [%s]: ", label, def)
	} else {
		fmt.Fprintf(os.Stderr, "%s: ", label)
	}
	text, _ := r.ReadString('\n')
	text = strings.TrimSpace(text)
	if text == "" {
		return def
	}
	return text
}

func askInt(r *bufio.Reader, label string, def int) (int, error) {
	text := ask(r, label, fmt.Sprint(def))
	n, err := strconv.Atoi(text)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("%s must be a positive number", label)
	}
	return n, nil
}

func askBool(r *bufio.Reader, label string, def bool) bool {
	defText := "y"
	if !def {
		defText = "n"
	}
	text := strings.ToLower(ask(r, label+" (y/n)", defText))
	return text == "y" || text == "yes" || text == "true"
}

func confirmText(expected string) error {
	if !isTerminal() {
		return fmt.Errorf("confirmation requires --yes or an interactive TTY; expected confirmation text %q", expected)
	}
	fmt.Fprintf(os.Stderr, "Live operation will modify infrastructure. Type %q to continue: ", expected)
	text, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	if strings.TrimSpace(text) != expected {
		return fmt.Errorf("confirmation did not match %q", expected)
	}
	return nil
}

func isTerminal() bool {
	stat, err := os.Stdin.Stat()
	return err == nil && (stat.Mode()&os.ModeCharDevice) != 0
}

func splitList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func printBootstrapPlan(nodes []bootstrap.Node) {
	fmt.Println("Bootstrap plan:")
	for _, n := range nodes {
		fmt.Printf("- %s %s@%s:%d auth=%s public_key=%s\n", n.Name, n.User, n.Address, n.Port, n.Auth, n.PublicKeyPath)
	}
}

func printOnboardPlan(cfg *config.Config, opts onboardOptions) {
	fmt.Printf("Onboarding plan for cluster %s (%s):\n", cfg.Cluster.Name, cfg.Cluster.Environment)
	for _, n := range cfg.Nodes {
		fmt.Printf("- %s private=%s public=%s roles=%s\n", n.Name, n.Address, n.PublicAddress, strings.Join(n.Roles, ","))
	}
	fmt.Printf("copy_ssh_key=%t install_docker=%t apply_firewall=%t install_components=%t dry_run=%t\n", opts.CopySSHKey, opts.InstallDocker, opts.ApplyFirewall, opts.InstallComponents, rootOpts.dryRun)
}

func inventoryFromConfig(cfg *config.Config) *inventory.Inventory {
	inv := &inventory.Inventory{ClusterName: cfg.Cluster.Name, Environment: cfg.Cluster.Environment, Datacenter: cfg.Cluster.Datacenter, InstallStatus: "pending", ComponentVersions: map[string]string{}, ServiceStatus: map[string]string{}}
	for _, n := range cfg.Nodes {
		inv.Nodes = append(inv.Nodes, inventory.Node{Name: n.Name, Roles: n.Roles, PrivateIP: n.Address, PublicIP: n.PublicAddress, SSH: inventory.SSHInfo{User: cfg.SSH.User, Port: cfg.SSH.Port, PrivateKeyPath: cfg.SSH.PrivateKeyPath}, Components: map[string]string{}, Services: map[string]string{}})
	}
	return inv
}

func inventoryForComponentCommand() (*inventory.Inventory, error) {
	if rootOpts.configPath != "" && rootOpts.dryRun {
		cfg, err := config.Load(rootOpts.configPath)
		if err != nil {
			return nil, err
		}
		return inventoryFromConfig(cfg), nil
	}
	return loadInventory()
}

func executorForConfig(cfg *config.Config) *sfssh.Executor {
	if rootOpts.dryRun || cfg == nil {
		return nil
	}
	return sfssh.NewExecutor(cfg.SSH.User, cfg.SSH.Port, cfg.SSH.PrivateKeyPath)
}

func domainPoolPath() string {
	return filepath.Join(stateFromCluster(), "domain-pool.yaml")
}

func domainPoolAuditPath() string {
	return filepath.Join(stateFromCluster(), "domain-pool-audit.jsonl")
}

func printJSONOrOutput(v any, err error) error {
	if rootOpts.output == "json" {
		return printJSON(v, err)
	}
	if err != nil {
		return err
	}
	return output(v)
}

func valueOr(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}
