package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/tokenmp/cli/internal/version"
)

// run executes the CLI with args and returns everything it wrote to stdout and
// stderr, plus the error the root command returned.
func run(t *testing.T, args ...string) (string, error) {
	t.Helper()

	root := NewRootCmd()

	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(args)

	err := root.Execute()

	return out.String(), err
}

func TestVersionFlag(t *testing.T) {
	want := version.Current().String()

	for _, arg := range []string{"--version", "-v"} {
		t.Run(arg, func(t *testing.T) {
			out, err := run(t, arg)
			if err != nil {
				t.Fatalf("tokenmp %s: %v", arg, err)
			}
			if !strings.Contains(out, want) {
				t.Errorf("tokenmp %s printed %q, want it to contain %q", arg, out, want)
			}
		})
	}
}

func TestVersionCommand(t *testing.T) {
	info := version.Current()

	out, err := run(t, "version")
	if err != nil {
		t.Fatalf("tokenmp version: %v", err)
	}

	for _, want := range []string{"tokenmp", info.Version, info.Platform} {
		if want == "" {
			continue
		}
		if !strings.Contains(out, want) {
			t.Errorf("tokenmp version printed %q, want it to contain %q", out, want)
		}
	}
}

func TestHelpCommandAndFlag(t *testing.T) {
	for _, arg := range []string{"help", "--help", "-h"} {
		t.Run(arg, func(t *testing.T) {
			out, err := run(t, arg)
			if err != nil {
				t.Fatalf("tokenmp %s: %v", arg, err)
			}
			for _, want := range []string{"tokenmp", "version", "--version"} {
				if !strings.Contains(out, want) {
					t.Errorf("tokenmp %s printed %q, want it to contain %q", arg, out, want)
				}
			}
		})
	}
}

func TestUnknownCommandFails(t *testing.T) {
	if _, err := run(t, "definitely-not-a-command"); err == nil {
		t.Fatal("tokenmp accepted an unknown command")
	}
}
