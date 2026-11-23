package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"runtime/debug"
	"strings"
)

var version = "dev"
var commit = "dev"

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

func VersionString() string {
	b, err := json.Marshal(map[string]string{"version": CurrentVersion(), "commit": commit})
	if err != nil {
		return `{"version":"dev","commit":"dev"}`
	}
	return string(b)
}

func runVersion(ctx context.Context, st Store, args []string, stdin io.Reader, stdout io.Writer) int {
	fmt.Fprintln(stdout, VersionString())
	return 0
}

func init() { Register("version", runVersion) }
