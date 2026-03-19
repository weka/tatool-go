package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/weka/tatool-go/internal/output"
	"github.com/weka/tatool-go/internal/script"
)

func newListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List available diagnostic scripts",
		RunE: func(cmd *cobra.Command, args []string) error {
			fsys, source, err := script.ResolveFS(scriptsPath)
			if err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "Scripts source: %s\n\n", source)

			scripts, err := script.LoadAll(fsys)
			if err != nil {
				return fmt.Errorf("loading scripts: %w", err)
			}

			for _, s := range scripts {
				typeTag := ""
				if s.Type != script.Parallel {
					typeTag = fmt.Sprintf(" [%s]", s.Type)
				}
				output.Cyan.Fprintf(os.Stdout, "%s", s.Filename)
				fmt.Fprintf(os.Stdout, ": %s%s\n", s.Description, typeTag)
			}

			fmt.Fprintf(os.Stderr, "\n%d scripts available\n", len(scripts))
			return nil
		},
	}
}
