package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/weka/tatool-go/internal/wizard"
	"golang.org/x/term"
)

var (
	logDir      string
	compression string
	scriptsPath string
)

func defaultLogDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "ta_runner_logs")
}

func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "tatool",
		Short: "Weka cluster diagnostic tool",
		Long:  "Run diagnostic scripts on Weka cluster nodes via SSH or Kubernetes.",
		RunE: func(cmd *cobra.Command, args []string) error {
			// If stdin is a terminal and no subcommand was given, launch the wizard
			if term.IsTerminal(int(os.Stdin.Fd())) {
				return runWizard()
			}
			return cmd.Help()
		},
	}

	root.PersistentFlags().StringVar(&logDir, "log-dir", defaultLogDir(), "directory to store logs")
	root.PersistentFlags().StringVar(&compression, "compression", "gz", "compression format for log bundle (gz or bz2)")
	root.PersistentFlags().StringVar(&scriptsPath, "scripts", "", "path to local scripts directory (overrides cached/embedded)")

	root.AddCommand(newListCmd())
	root.AddCommand(newRunCmd())
	root.AddCommand(newUpdateScriptsCmd())
	root.AddCommand(newVersionCmd())

	return root
}

func runWizard() error {
	cfg, err := wizard.RunWizard(defaultLogDir())
	if err != nil {
		return err
	}

	cfg.LogDir = logDir
	if cfg.LogDir == "" {
		cfg.LogDir = defaultLogDir()
	}
	cfg.Compression = compression
	cfg.ScriptsPath = scriptsPath

	var runErr error
	if cfg.K8s {
		runErr = RunK8s(cfg)
	} else {
		runErr = RunSSH(cfg)
	}

	// Print the rerun command hint only on success
	if runErr == nil {
		rerun := wizard.BuildRerunCommand(cfg)
		fmt.Fprintf(os.Stderr, "\n\033[1m💡 To run this faster next time:\033[0m\n   %s\n\n", rerun)
	}

	return runErr
}
