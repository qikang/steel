// Package cmd implements the steel CLI as a cobra command tree.
package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "steel",
	Short: "Steel — lightweight ops tool: file distribution + remote shell",
	Long: `Steel is a Go-based ops tool inspired by Ansible's copy and shell modules.
It distributes local files to remote servers and runs shell commands as if
you had SSH'd in yourself — preserving the target user's PATH and env.`,
	SilenceUsage:  true,
	SilenceErrors: true,
}

// Execute is the entrypoint used by main.go.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
