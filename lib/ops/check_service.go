package ops

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const serviceRemnantLabel = "dev.flint.engine"

var (
	serviceRemnantOSFn       = func() string { return runtime.GOOS }
	serviceRemnantPathFn     = serviceRemnantPathDefault
	serviceRemnantReadFileFn = os.ReadFile
)

func serviceRemnantPathDefault() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", err
	}
	switch serviceRemnantOSFn() {
	case "darwin":
		return filepath.Join(home, "Library", "LaunchAgents", serviceRemnantLabel+".plist"), nil
	case "linux":
		return filepath.Join(home, ".config", "systemd", "user", serviceRemnantLabel+".service"), nil
	}
	return "", nil
}

func CheckServiceRemnant() DoctorCheck {
	c := DoctorCheck{Name: "service-remnant"}
	osName := serviceRemnantOSFn()
	if osName != "darwin" && osName != "linux" {
		c.Status = "ok"
		c.Detail = "no launchd/systemd user service on " + osName
		return c
	}
	path, err := serviceRemnantPathFn()
	if err != nil || path == "" {
		c.Status = "ok"
		c.Detail = "service definition path unavailable — verdict deferred"
		return c
	}
	data, err := serviceRemnantReadFileFn(path)
	if err != nil {
		c.Status = "ok"
		c.Detail = "no service definition registered"
		return c
	}
	body := string(data)
	if containsTempPath(body) {
		c.Status = "warn"
		c.Detail = path + ": service definition references a temporary path — leftover registration"
		c.Fix = "fix: re-register with flint setup --engine native — or remove the stale definition (launchctl bootout gui/$(id -u)/" + serviceRemnantLabel + " · systemctl --user disable flint-engine.service)"
		return c
	}
	c.Status = "ok"
	c.Detail = path
	return c
}

func containsTempPath(body string) bool {
	if strings.Contains(body, "/tmp/") || strings.Contains(body, "/var/folders") {
		return true
	}
	tmp := os.TempDir()
	return tmp != "" && strings.Contains(body, tmp+string(filepath.Separator))
}
