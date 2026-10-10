// Package cmd implements the tokenmp command line interface.
package cmd

import (
	"context"

	"github.com/charmbracelet/fang"
	"github.com/spf13/cobra"

	"github.com/tokenmp/cli/internal/version"
)

// Execute builds the command tree and runs it. It returns the error reported by
// the executed command, if any; fang has already printed a styled message for
// it by then.
func Execute(ctx context.Context) error {
	info := version.Current()

	return fang.Execute(
		ctx,
		NewRootCmd(),
		fang.WithVersion(info.Version),
		fang.WithCommit(info.Commit),
		// Only the commands documented in the README are shipped, so the
		// generated `completion` and hidden `man` commands are turned off.
		// Drop these two options to get them back.
		fang.WithoutCompletions(),
		fang.WithoutManpage(),
	)
}

// NewRootCmd assembles the whole command tree.
func NewRootCmd() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "tokenmp",
		Short: "TokenMP command line interface",
		Long: `TokenMP command line interface.

Run ` + "`tokenmp help`" + ` for the full command list, or ` + "`tokenmp help <command>`" + `
for the details of a single command.`,
		Example: `  # Print the version of this build
  tokenmp version

  # The same, in its one-line form
  tokenmp --version

  # Learn what else this CLI can do
  tokenmp help`,
		Args: cobra.NoArgs,
		// Version is what `--version`/`-v` reports; fang refreshes it from the
		// same build metadata when Execute runs.
		Version: version.Current().String(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}

	rootCmd.AddCommand(newVersionCmd())

	return rootCmd
}
