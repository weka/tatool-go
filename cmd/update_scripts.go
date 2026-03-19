package cmd

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"github.com/weka/tatool-go/internal/script"
)

func newUpdateScriptsCmd() *cobra.Command {
	var repo, branch string

	cmd := &cobra.Command{
		Use:   "update-scripts",
		Short: "Fetch latest diagnostic scripts from weka/tools",
		Long:  "Downloads the latest ta scripts from github.com/weka/tools and caches them locally.",
		RunE: func(cmd *cobra.Command, args []string) error {
			lastUpdate := script.LastUpdated()
			if !lastUpdate.IsZero() {
				fmt.Printf("Last updated: %s\n", lastUpdate.Format(time.RFC3339))
			}

			fmt.Printf("Fetching scripts from %s (branch: %s)...\n", repo, branch)

			count, err := script.SyncFromGitHub(repo, branch)
			if err != nil {
				return fmt.Errorf("sync failed: %w", err)
			}

			fmt.Printf("Successfully cached %d scripts\n", count)
			return nil
		},
	}

	cmd.Flags().StringVar(&repo, "repo", "weka/tools", "GitHub repository (owner/repo)")
	cmd.Flags().StringVar(&branch, "branch", "master", "branch or tag to fetch")

	return cmd
}
