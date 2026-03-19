package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/weka/tatool-go/internal/config"
	"github.com/weka/tatool-go/internal/discovery"
	"github.com/weka/tatool-go/internal/executor"
	"github.com/weka/tatool-go/internal/output"
	"github.com/weka/tatool-go/internal/runner"
	"github.com/weka/tatool-go/internal/script"
	"github.com/weka/tatool-go/internal/selector"
	"k8s.io/klog/v2"
)

func newRunCmd() *cobra.Command {
	cfg := &config.Config{}
	var ipsStr, scriptNumStr string

	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run diagnostic scripts on targets",
		Long:  "Run diagnostic scripts on Weka cluster nodes via SSH or Kubernetes.",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Parse IPs
			if ipsStr != "" {
				cfg.IPs = strings.Split(ipsStr, ",")
			}

			// Parse script numbers
			if scriptNumStr != "" {
				for _, s := range strings.Split(scriptNumStr, ",") {
					s = strings.TrimSpace(s)
					n, err := strconv.Atoi(s)
					if err != nil {
						return fmt.Errorf("invalid script number %q: %w", s, err)
					}
					cfg.ScriptNums = append(cfg.ScriptNums, n)
				}
			}

			cfg.LogDir = logDir
			cfg.Compression = compression
			cfg.ScriptsPath = scriptsPath

			// Validate
			if cfg.K8s {
				if cfg.Namespace == "" {
					return fmt.Errorf("--k8s-namespace is required when using --k8s")
				}
				return RunK8s(cfg)
			}

			if len(cfg.IPs) == 0 {
				return fmt.Errorf("--ips is required (or use --k8s for Kubernetes mode)")
			}
			if cfg.User == "" {
				return fmt.Errorf("--user is required for SSH mode")
			}

			return RunSSH(cfg)
		},
	}

	// SSH flags
	cmd.Flags().StringVar(&ipsStr, "ips", "", "comma-separated list of IP addresses")
	cmd.Flags().StringVar(&cfg.User, "user", "", "SSH username")
	cmd.Flags().StringVar(&cfg.Password, "password", "", "SSH password")
	cmd.Flags().StringVar(&cfg.PasswordEnv, "password-env", "", "environment variable containing SSH password")
	cmd.Flags().StringVar(&cfg.PasswordFile, "password-file", "", "file containing SSH password")
	cmd.Flags().StringVar(&cfg.KeyFile, "key", "", "path to SSH private key")
	cmd.Flags().BoolVar(&cfg.UseDzdo, "dzdo", false, "use dzdo instead of sudo")

	// K8s flags
	cmd.Flags().BoolVar(&cfg.K8s, "k8s", false, "enable Kubernetes mode")
	cmd.Flags().StringVar(&cfg.Namespace, "k8s-namespace", "", "Kubernetes namespace")
	cmd.Flags().StringVar(&cfg.ClusterName, "cluster-name", "cluster1", "base name of Kubernetes pods")
	cmd.Flags().StringVar(&cfg.Kubeconfig, "kubeconfig", "", "path to kubeconfig file")

	// Script selection
	cmd.Flags().StringVar(&scriptNumStr, "script-num", "", "comma-separated script numbers to run (e.g. 001,005,999)")
	cmd.Flags().BoolVar(&cfg.Interactive, "interactive", false, "interactive script selection UI")

	return cmd
}

// setupContext creates a cancellable context that suppresses klog on interrupt.
func setupContext() (context.Context, context.CancelFunc) {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)

	// Suppress klog output to avoid "read/write on closed pipe" spam on Ctrl+C
	klog.SetOutput(io.Discard)
	klog.LogToStderr(false)

	return ctx, cancel
}

// handleRunError returns nil for context cancellation (clean Ctrl+C) or the original error.
func handleRunError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		fmt.Fprintf(os.Stderr, "\nInterrupted.\n")
		return nil
	}
	return err
}

func RunK8s(cfg *config.Config) error {
	ctx, cancel := setupContext()
	defer cancel()

	// Resolve scripts
	fsys, source, err := script.ResolveFS(cfg.ScriptsPath)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Scripts source: %s\n", source)

	scripts, err := script.LoadAll(fsys)
	if err != nil {
		return fmt.Errorf("loading scripts: %w", err)
	}

	// Script selection
	if cfg.Interactive {
		selected, err := selector.InteractiveSelect(scripts)
		if err != nil {
			return err
		}
		scripts = selected
	} else if len(cfg.ScriptNums) > 0 {
		scripts = script.FilterByNumbers(scripts, cfg.ScriptNums)
		if len(scripts) == 0 {
			return fmt.Errorf("no scripts matched the specified numbers")
		}
	}

	fmt.Fprintf(os.Stderr, "Selected %d scripts\n", len(scripts))

	// Discover pods
	fmt.Fprintf(os.Stderr, "Discovering pods in namespace %s...\n", cfg.Namespace)
	pods, err := discovery.DiscoverK8sPods(ctx, cfg.Namespace, cfg.ClusterName, cfg.Kubeconfig)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Found %d pods: %s\n\n", len(pods), strings.Join(pods, ", "))

	// Create executor
	exec, err := executor.NewK8sExecutor(cfg.Namespace, cfg.Kubeconfig)
	if err != nil {
		return fmt.Errorf("creating K8s executor: %w", err)
	}
	defer exec.Close()

	// Create logger
	logger, err := output.NewLogger(cfg.LogDir)
	if err != nil {
		return err
	}

	// Run
	results, runErr := runner.Run(ctx, runner.RunConfig{
		Targets:   pods,
		Scripts:   scripts,
		ScriptsFS: fsys,
		Exec:      exec,
		Logger:    logger,
		UseDzdo:   false, // not applicable in K8s
	})

	// Always print summary even if there was an error
	if len(results) > 0 {
		runner.PrintSummary(results)
	}

	// Bundle logs
	bundle, bundleErr := output.BundleLogs(cfg.LogDir, cfg.Compression)
	if bundleErr == nil {
		fmt.Fprintf(os.Stderr, "\nLogs bundled: %s\n", bundle)
	}

	return handleRunError(runErr)
}

func RunSSH(cfg *config.Config) error {
	ctx, cancel := setupContext()
	defer cancel()

	// Resolve scripts
	fsys, source, err := script.ResolveFS(cfg.ScriptsPath)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Scripts source: %s\n", source)

	scripts, err := script.LoadAll(fsys)
	if err != nil {
		return fmt.Errorf("loading scripts: %w", err)
	}

	// Script selection
	if cfg.Interactive {
		selected, err := selector.InteractiveSelect(scripts)
		if err != nil {
			return err
		}
		scripts = selected
	} else if len(cfg.ScriptNums) > 0 {
		scripts = script.FilterByNumbers(scripts, cfg.ScriptNums)
		if len(scripts) == 0 {
			return fmt.Errorf("no scripts matched the specified numbers")
		}
	}

	fmt.Fprintf(os.Stderr, "Selected %d scripts\n", len(scripts))

	// Create SSH executor
	exec, err := executor.NewSSHExecutor(cfg.User, cfg.Password, cfg.PasswordEnv, cfg.PasswordFile, cfg.KeyFile, cfg.UseDzdo)
	if err != nil {
		return fmt.Errorf("creating SSH executor: %w", err)
	}
	defer exec.Close()

	// Auto-expand IPs if only one provided
	ips := cfg.IPs
	if len(ips) == 1 {
		fmt.Fprintf(os.Stderr, "Single IP provided, attempting to discover cluster IPs from %s...\n", ips[0])
		expanded, err := discovery.ExpandIPsFromSeed(ips[0], exec.SSHClientConfig())
		if err != nil {
			fmt.Fprintf(os.Stderr, "IP expansion failed (%v), using single IP\n", err)
		} else if len(expanded) > 1 {
			ips = expanded
			fmt.Fprintf(os.Stderr, "Expanded to %d IPs: %s\n", len(ips), strings.Join(ips, ", "))
		}
	}

	fmt.Fprintf(os.Stderr, "Targets: %s\n\n", strings.Join(ips, ", "))

	// Create logger
	logger, err := output.NewLogger(cfg.LogDir)
	if err != nil {
		return err
	}

	// Run
	results, runErr := runner.Run(ctx, runner.RunConfig{
		Targets:   ips,
		Scripts:   scripts,
		ScriptsFS: fsys,
		Exec:      exec,
		Logger:    logger,
		UseDzdo:   cfg.UseDzdo,
	})

	if len(results) > 0 {
		runner.PrintSummary(results)
	}

	bundle, bundleErr := output.BundleLogs(cfg.LogDir, cfg.Compression)
	if bundleErr == nil {
		fmt.Fprintf(os.Stderr, "\nLogs bundled: %s\n", bundle)
	}

	return handleRunError(runErr)
}
