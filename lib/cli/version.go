// version.go — flint version: one-line build metadata JSON (ldflags injection, default dev).
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"runtime/debug"
	"strings"
)

// version and commit — -ldflags -X injection points from the Makefile build (dev fallback when not injected).
var version = "dev"
var commit = "dev"

// CurrentVersion — returns the effective version string. Builds without ldflags
// injection (`go install github.com/synuns/flint/cmd/flint@vX.Y.Z`)
// fall back to the module version from debug.ReadBuildInfo — self-reported
// versioning on the go install distribution path. Returns "dev" if unresolvable.
func CurrentVersion() string {
	if validVersion(version) {
		return version
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		if v := strings.TrimPrefix(bi.Main.Version, "v"); validVersion(v) {
			return v
		}
	}
	return version
}

// VersionString — one-line {"version":...,"commit":...} JSON.
func VersionString() string {
	b, err := json.Marshal(map[string]string{"version": CurrentVersion(), "commit": commit})
	if err != nil { // map[string]string is effectively infallible — precautionary path
		return `{"version":"dev","commit":"dev"}`
	}
	return string(b)
}

// runVersion — prints the version. No store needed (pure local computation).
func runVersion(ctx context.Context, st Store, args []string, stdin io.Reader, stdout io.Writer) int {
	fmt.Fprintln(stdout, VersionString())
	return 0
}

func init() { Register("version", runVersion) }
