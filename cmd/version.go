package cmd

import (
	"fmt"
	"os"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/exp/charmtone"
	"github.com/spf13/cobra"

	"github.com/tokenmp/cli/internal/version"
)

// labelWidth keeps the labels of the version table aligned, `platform:` being
// the longest of them.
const labelWidth = len("platform:")

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version of this build",
		Long: `Print the version of this build.

The reported commit, build time and Go version are the ones recorded when the
binary was produced; an unstamped build falls back to the metadata the Go
toolchain embedded.`,
		Example: `  tokenmp version
  tokenmp --version`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			printVersion(cmd)
			return nil
		},
	}
}

// printVersion writes the build metadata as a small key/value table, styled with
// the same palette fang uses for help output.
func printVersion(cmd *cobra.Command) {
	info := version.Current()

	name := lipgloss.NewStyle().Bold(true).Foreground(charmtone.Malibu)
	number := lipgloss.NewStyle().Bold(true).Foreground(charmtone.Charple)
	label := lipgloss.NewStyle().Foreground(charmtone.Squid).Width(labelWidth)
	value := lipgloss.NewStyle().Foreground(charmtone.Charcoal)

	// Downsample to the capabilities of the terminal we are writing to.
	out := colorprofile.NewWriter(cmd.OutOrStdout(), os.Environ())

	fmt.Fprintf(out, "%s %s\n\n", name.Render("tokenmp"), number.Render(info.Version))
	for _, row := range [][2]string{
		{"commit:", info.Commit},
		{"built:", info.Date},
		{"go:", info.GoVersion},
		{"platform:", info.Platform},
	} {
		if row[1] == "" {
			continue
		}
		fmt.Fprintf(out, "%s %s\n", label.Render(row[0]), value.Render(row[1]))
	}
}
