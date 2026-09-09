// Package version exposes build metadata injected at compile time via
// -ldflags (see the Makefile), so binaries can report exactly what was built.
package version

import "fmt"

var (
	Version   = "dev"
	Commit    = "none"
	BuildDate = "unknown"
)

// String renders the build metadata in a single human-readable line, used in
// startup logs and the --version flag of every cmd/ binary.
func String() string {
	return fmt.Sprintf("%s (commit=%s, built=%s)", Version, Commit, BuildDate)
}
