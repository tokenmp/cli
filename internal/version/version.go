// Package version exposes the build metadata of the tokenmp CLI.
package version

import (
	"runtime"
	"runtime/debug"
	"strings"
)

// Build metadata injected by the linker at build time:
//
//	go build -ldflags "-X github.com/tokenmp/cli/internal/version.Version=v1.0.0"
//
// The zero values are replaced with whatever the Go toolchain recorded in the
// binary (VCS revision and time), so a plain `go build` still reports something
// meaningful.
var (
	// Version is the semantic version of the build.
	Version = ""
	// Commit is the git revision the binary was built from.
	Commit = ""
	// Date is the RFC 3339 timestamp of the build.
	Date = ""
)

// Info describes a single build of the CLI.
type Info struct {
	Version   string
	Commit    string
	Date      string
	GoVersion string
	Platform  string
}

// Current returns the build metadata of the running binary.
func Current() Info {
	info := Info{
		Version:   Version,
		Commit:    Commit,
		Date:      Date,
		GoVersion: runtime.Version(),
		Platform:  runtime.GOOS + "/" + runtime.GOARCH,
	}

	build, ok := debug.ReadBuildInfo()
	if !ok {
		return info
	}

	for _, setting := range build.Settings {
		switch setting.Key {
		case "vcs.revision":
			if info.Commit == "" {
				info.Commit = setting.Value
			}
		case "vcs.time":
			if info.Date == "" {
				info.Date = setting.Value
			}
		}
	}

	if info.Version == "" {
		if v := build.Main.Version; v != "" && v != "(devel)" {
			info.Version = v
		}
	}

	return info
}

// ShortCommit returns the abbreviated commit hash, or an empty string when the
// build carries no VCS information.
func (i Info) ShortCommit() string {
	const abbreviation = 7
	if len(i.Commit) < abbreviation {
		return i.Commit
	}
	return i.Commit[:abbreviation]
}

// String renders the one-line form used by `tokenmp --version`.
func (i Info) String() string {
	s := i.Version
	if s == "" {
		s = "unknown (built from source)"
	}
	if short := i.ShortCommit(); short != "" && short != s {
		s += " (" + short + ")"
	}
	return strings.TrimSpace(s)
}
