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
	var ipsStr, scriptNumStr, instanceIDsStr string

	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run diagnostic scripts on targets",
		Long:  "Run diagnostic scripts on Weka cluster nodes via SSH or Kubernetes.",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Parse IPs
			if ipsStr != "" {
				cfg.IPs = strings.Split(ipsStr, ",")
			}

			// Parse instance IDs
			if instanceIDsStr != "" {
				for _, id := range strings.Split(instanceIDsStr, ",") {
					id = strings.TrimSpace(id)
					if id != "" {
						cfg.InstanceIDs = append(cfg.InstanceIDs, id)
					}
				}
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

			if cfg.SSM {
				return RunSSM(cfg)
			}

			if len(cfg.IPs) == 0 {
				return fmt.Errorf("--ips is required (or use --k8s / --ssm)")
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

	// SSM flags
	cmd.Flags().BoolVar(&cfg.SSM, "ssm", false, "enable AWS SSM mode")
	cmd.Flags().StringVar(&instanceIDsStr, "instance-ids", "", "comma-separated EC2 instance IDs (SSM mode; auto-discovered if omitted)")
	cmd.Flags().StringVar(&cfg.AWSRegion, "aws-region", "", "AWS region (SSM mode; auto-detected from instance metadata if omitted)")
	cmd.Flags().StringVar(&cfg.AWSProfile, "aws-profile", "", "AWS credentials profile (SSM mode; uses instance profile if omitted)")

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

	// Write test_results.json
	if len(results) > 0 {
		if err := output.WriteResultsData(cfg.LogDir, runner.BuildResultsJSON(results)); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not write test_results.json: %v\n", err)
		} else {
			fmt.Fprintf(os.Stderr, "Results written: %s/test_results.json\n", cfg.LogDir)
		}
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

	if len(results) > 0 {
		if err := output.WriteResultsData(cfg.LogDir, runner.BuildResultsJSON(results)); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not write test_results.json: %v\n", err)
		} else {
			fmt.Fprintf(os.Stderr, "Results written: %s/test_results.json\n", cfg.LogDir)
		}
	}

	bundle, bundleErr := output.BundleLogs(cfg.LogDir, cfg.Compression)
	if bundleErr == nil {
		fmt.Fprintf(os.Stderr, "\nLogs bundled: %s\n", bundle)
	}

	return handleRunError(runErr)
}


func RunSSM(cfg *config.Config) error {
	ctx, cancel := setupContext()
	defer cancel()

	// Auto-discover instance IDs if not provided.
	// Get Weka backend IPs from the local CLI, then resolve them to SSM
	// instance IDs via DescribeInstanceInformation.
	if len(cfg.InstanceIDs) == 0 {
		ips := discovery.WekaBackendIPs()
		if len(ips) == 0 {
			return fmt.Errorf("no --instance-ids provided and weka CLI returned no backend IPs; " +
				"pass --instance-ids i-xxx,i-yyy")
		}
		fmt.Fprintf(os.Stderr, "Resolving %d Weka backend IPs to SSM instance IDs...\n", len(ips))
		found, err := discovery.MatchInstancesByIP(ctx, cfg.AWSRegion, ips)
		if err != nil {
			return fmt.Errorf("SSM IP-to-instance resolution: %w", err)
		}
		if len(found) == 0 {
			return fmt.Errorf("no SSM-managed instances found matching backend IPs %s; "+
				"ensure the SSM agent is running and the instance profile has ssm:DescribeInstanceInformation",
				strings.Join(ips, ", "))
		}
		for _, inst := range found {
			cfg.InstanceIDs = append(cfg.InstanceIDs, inst.InstanceID)
		}
		fmt.Fprintf(os.Stderr, "Resolved to %d instance IDs: %s\n", len(found), strings.Join(cfg.InstanceIDs, ", "))
	}

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
	fmt.Fprintf(os.Stderr, "Targets: %s\n\n", strings.Join(cfg.InstanceIDs, ", "))

	// Create SSM executor
	ssmExec, err := executor.NewSSMExecutor(cfg.AWSRegion, cfg.AWSProfile)
	if err != nil {
		return fmt.Errorf("creating SSM executor: %w", err)
	}
	defer ssmExec.Close()

	// Create logger
	logger, err := output.NewLogger(cfg.LogDir)
	if err != nil {
		return err
	}

	// Run
	results, runErr := runner.Run(ctx, runner.RunConfig{
		Targets:   cfg.InstanceIDs,
		Scripts:   scripts,
		ScriptsFS: fsys,
		Exec:      ssmExec,
		Logger:    logger,
		UseDzdo:   false, // SSM runs as root; sudo is not needed
	})

	if len(results) > 0 {
		runner.PrintSummary(results)
	}

	if len(results) > 0 {
		if err := output.WriteResultsData(cfg.LogDir, runner.BuildResultsJSON(results)); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not write test_results.json: %v\n", err)
		} else {
			fmt.Fprintf(os.Stderr, "Results written: %s/test_results.json\n", cfg.LogDir)
		}
	}

	bundle, bundleErr := output.BundleLogs(cfg.LogDir, cfg.Compression)
	if bundleErr == nil {
		fmt.Fprintf(os.Stderr, "\nLogs bundled: %s\n", bundle)
	}

	return handleRunError(runErr)
}
