package wizard

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/weka/tatool-go/internal/config"
	"github.com/weka/tatool-go/internal/script"
	"github.com/weka/tatool-go/internal/selector"
)

// RunWizard walks the user through an interactive setup and returns a populated Config.
func RunWizard(defaultLogDir string) (*config.Config, error) {
	cfg := &config.Config{
		LogDir:      defaultLogDir,
		Compression: "gz",
		ClusterName: "cluster1",
	}

	// Step 1: Mode selection
	mode, err := selectMode()
	if err != nil {
		return nil, err
	}

	if mode == "k8s" {
		cfg.K8s = true
		if err := wizardK8s(cfg); err != nil {
			return nil, err
		}
	} else {
		if err := wizardSSH(cfg); err != nil {
			return nil, err
		}
	}

	// Script selection
	if err := wizardScripts(cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

func selectMode() (string, error) {
	var mode string

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("How would you like to connect?").
				Options(
					huh.NewOption("Kubernetes", "k8s"),
					huh.NewOption("Traditional Weka (SSH)", "ssh"),
				).
				Value(&mode),
		),
	)

	if err := form.Run(); err != nil {
		return "", fmt.Errorf("mode selection: %w", err)
	}
	return mode, nil
}

func wizardK8s(cfg *config.Config) error {
	// Fetch namespaces
	namespaces, err := fetchNamespaces(cfg.Kubeconfig)
	if err != nil {
		return fmt.Errorf("fetching namespaces: %w", err)
	}

	if len(namespaces) == 0 {
		return fmt.Errorf("no namespaces found — is kubectl configured?")
	}

	// Select namespace
	options := make([]huh.Option[string], len(namespaces))
	for i, ns := range namespaces {
		options[i] = huh.NewOption(ns, ns)
	}

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Which namespace has your Weka pods?").
				Options(options...).
				Value(&cfg.Namespace),
		),
	)

	if err := form.Run(); err != nil {
		return fmt.Errorf("namespace selection: %w", err)
	}

	// Fetch Weka clusters in the selected namespace
	clusters, err := fetchWekaClusters(cfg.Namespace, cfg.Kubeconfig)
	if err != nil {
		return fmt.Errorf("fetching clusters: %w", err)
	}

	if len(clusters) == 0 {
		// Fall back to manual entry
		var clusterName string
		form := huh.NewForm(
			huh.NewGroup(
				huh.NewInput().
					Title("No WekaCluster resources found. Enter cluster name manually:").
					Placeholder("cluster1").
					Value(&clusterName),
			),
		)
		if err := form.Run(); err != nil {
			return err
		}
		if clusterName == "" {
			clusterName = "cluster1"
		}
		cfg.ClusterName = clusterName
		return nil
	}

	if len(clusters) == 1 {
		cfg.ClusterName = clusters[0]
		fmt.Printf("Found cluster: %s\n", clusters[0])
		return nil
	}

	// Multiple clusters — let them pick
	clusterOptions := make([]huh.Option[string], len(clusters))
	for i, c := range clusters {
		clusterOptions[i] = huh.NewOption(c, c)
	}

	form = huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Which cluster?").
				Options(clusterOptions...).
				Value(&cfg.ClusterName),
		),
	)

	return form.Run()
}

func wizardSSH(cfg *config.Config) error {
	var ipsStr, authMethod string

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("Enter IP address (or comma-separated IPs)").
				Placeholder("10.0.0.1").
				Value(&ipsStr),
			huh.NewInput().
				Title("SSH username").
				Placeholder("root").
				Value(&cfg.User),
			huh.NewSelect[string]().
				Title("Authentication method").
				Options(
					huh.NewOption("SSH key file", "key"),
					huh.NewOption("Password", "password"),
					huh.NewOption("SSH agent", "agent"),
				).
				Value(&authMethod),
		),
	)

	if err := form.Run(); err != nil {
		return fmt.Errorf("SSH details: %w", err)
	}

	if ipsStr != "" {
		cfg.IPs = strings.Split(ipsStr, ",")
		for i := range cfg.IPs {
			cfg.IPs[i] = strings.TrimSpace(cfg.IPs[i])
		}
	}

	switch authMethod {
	case "key":
		var keyPath string
		form := huh.NewForm(
			huh.NewGroup(
				huh.NewInput().
					Title("Path to SSH private key").
					Placeholder("~/.ssh/id_ed25519").
					Value(&keyPath),
			),
		)
		if err := form.Run(); err != nil {
			return err
		}
		cfg.KeyFile = keyPath

	case "password":
		var pw string
		form := huh.NewForm(
			huh.NewGroup(
				huh.NewInput().
					Title("SSH password").
					EchoMode(huh.EchoModePassword).
					Value(&pw),
			),
		)
		if err := form.Run(); err != nil {
			return err
		}
		cfg.Password = pw
	}
	// "agent" needs no additional input

	return nil
}

func wizardScripts(cfg *config.Config) error {
	var scriptMode string

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Which scripts do you want to run?").
				Options(
					huh.NewOption("Run all scripts", "all"),
					huh.NewOption("Choose scripts to run", "choose"),
				).
				Value(&scriptMode),
		),
	)

	if err := form.Run(); err != nil {
		return fmt.Errorf("script mode: %w", err)
	}

	if scriptMode == "choose" {
		cfg.Interactive = true

		// Load scripts so the selector can present them
		fsys, _, err := script.ResolveFS(cfg.ScriptsPath)
		if err != nil {
			return err
		}
		scripts, err := script.LoadAll(fsys)
		if err != nil {
			return err
		}

		selected, err := selector.InteractiveSelect(scripts)
		if err != nil {
			return err
		}

		for _, s := range selected {
			cfg.ScriptNums = append(cfg.ScriptNums, s.Number)
		}
		cfg.Interactive = false // we already resolved to script numbers
	}

	return nil
}

// fetchNamespaces runs kubectl to get all namespace names.
func fetchNamespaces(kubeconfig string) ([]string, error) {
	args := []string{"get", "namespaces", "-o", "jsonpath={.items[*].metadata.name}"}
	if kubeconfig != "" {
		args = append([]string{"--kubeconfig", kubeconfig}, args...)
	}

	out, err := exec.Command("kubectl", args...).Output()
	if err != nil {
		return nil, err
	}

	raw := strings.TrimSpace(string(out))
	if raw == "" {
		return nil, nil
	}

	return strings.Fields(raw), nil
}

// fetchWekaClusters runs kubectl to get WekaCluster resources in a namespace.
func fetchWekaClusters(namespace, kubeconfig string) ([]string, error) {
	args := []string{"get", "wekacluster", "-n", namespace, "-o", "jsonpath={.items[*].metadata.name}"}
	if kubeconfig != "" {
		args = append([]string{"--kubeconfig", kubeconfig}, args...)
	}

	out, err := exec.Command("kubectl", args...).Output()
	if err != nil {
		// CRD might not exist — not a fatal error
		return nil, nil
	}

	raw := strings.TrimSpace(string(out))
	if raw == "" {
		return nil, nil
	}

	return strings.Fields(raw), nil
}

// BuildRerunCommand generates the equivalent CLI command from a Config.
func BuildRerunCommand(cfg *config.Config) string {
	var parts []string
	parts = append(parts, "./tatool run")

	if cfg.K8s {
		parts = append(parts, "--k8s")
		parts = append(parts, fmt.Sprintf("--k8s-namespace %s", cfg.Namespace))
		parts = append(parts, fmt.Sprintf("--cluster-name %s", cfg.ClusterName))
		if cfg.Kubeconfig != "" {
			parts = append(parts, fmt.Sprintf("--kubeconfig %s", cfg.Kubeconfig))
		}
	} else {
		if len(cfg.IPs) > 0 {
			parts = append(parts, fmt.Sprintf("--ips %s", strings.Join(cfg.IPs, ",")))
		}
		if cfg.User != "" {
			parts = append(parts, fmt.Sprintf("--user %s", cfg.User))
		}
		if cfg.KeyFile != "" {
			parts = append(parts, fmt.Sprintf("--key %s", cfg.KeyFile))
		}
		if cfg.UseDzdo {
			parts = append(parts, "--dzdo")
		}
	}

	if len(cfg.ScriptNums) > 0 {
		nums := make([]string, len(cfg.ScriptNums))
		for i, n := range cfg.ScriptNums {
			nums[i] = fmt.Sprintf("%03d", n)
		}
		parts = append(parts, fmt.Sprintf("--script-num %s", strings.Join(nums, ",")))
	}

	return strings.Join(parts, " ")
}
