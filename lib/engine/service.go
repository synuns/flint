package engine

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

const launchdLabel = "dev.flint.engine"

var (
	serviceOS    = func() string { return runtime.GOOS }
	userHomeDir  = os.UserHomeDir
	launchctlCmd = func(args ...string) error { return runManagerCmd("launchctl", args...) }
	systemctlCmd = func(args ...string) error { return runManagerCmd("systemctl", args...) }
)

func runManagerCmd(name string, args ...string) error {
	bin, err := exec.LookPath(name)
	if err != nil {
		return fmt.Errorf("engine: %s not found in PATH — the service manager is required to register the native engine; install it or start the engine manually with 'flint engine start': %w", name, err)
	}
	c := exec.Command(bin, args...)
	out, err := c.CombinedOutput()
	if err != nil {
		return fmt.Errorf("engine: %s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func serviceFilePath() (string, error) {
	home, err := userHomeDir()
	if err != nil || home == "" {
		return "", errors.New("engine: user home directory unavailable")
	}
	switch serviceOS() {
	case "darwin":
		return filepath.Join(home, "Library", "LaunchAgents", launchdLabel+".plist"), nil
	case "linux":
		return filepath.Join(home, ".config", "systemd", "user", "flint-engine.service"), nil
	}
	return "", fmt.Errorf("%w: %s has no supported service manager", ErrUnsupportedPlatform, serviceOS())
}

func currentBinaryPath() string {
	return filepath.Join(engineHomeDir(), "engine", "current", "doltgres")
}

func logFilePaths() (string, string) {
	dir := filepath.Join(engineHomeDir(), "engine", "logs")
	return filepath.Join(dir, "engine.out.log"), filepath.Join(dir, "engine.err.log")
}

func plistContent(bin, dataDir, outLog, errLog string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>` + launchdLabel + `</string>
	<key>WorkingDirectory</key>
	<string>` + filepath.Dir(bin) + `</string>
	<key>ProgramArguments</key>
	<array>
		<string>` + bin + `</string>
		<string>-data-dir</string>
		<string>` + dataDir + `</string>
	</array>
	<key>KeepAlive</key>
	<true/>
	<key>StandardOutPath</key>
	<string>` + outLog + `</string>
	<key>StandardErrorPath</key>
	<string>` + errLog + `</string>
</dict>
</plist>
`
}

func unitContent(bin, dataDir, outLog, errLog string) string {
	return `[Unit]
Description=flint native engine

[Service]
WorkingDirectory=` + filepath.Dir(bin) + `
ExecStart=` + bin + ` -data-dir ` + dataDir + `
Restart=always
StandardOutput=append:` + outLog + `
StandardError=append:` + errLog + `

[Install]
WantedBy=default.target
`
}

func guiTarget() string {
	return "gui/" + strconv.Itoa(os.Getuid())
}

func guiServiceTarget() string {
	return guiTarget() + "/" + launchdLabel
}

func Register() error {
	path, err := serviceFilePath()
	if err != nil {
		return err
	}
	bin := currentBinaryPath()
	if _, err := os.Stat(bin); err != nil {
		return fmt.Errorf("engine: native binary not found at %s — run flint setup --engine native to install it first", bin)
	}
	outLog, errLog := logFilePaths()
	if err := os.MkdirAll(filepath.Dir(outLog), 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	var content string
	if serviceOS() == "darwin" {
		content = plistContent(bin, filepath.Join(filepath.Dir(bin), "data"), outLog, errLog)
	} else {
		content = unitContent(bin, filepath.Join(filepath.Dir(bin), "data"), outLog, errLog)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return err
	}
	if serviceOS() == "darwin" {
		return launchctlCmd("bootstrap", guiTarget(), path)
	}
	if err := systemctlCmd("--user", "daemon-reload"); err != nil {
		return err
	}
	return systemctlCmd("--user", "enable", "flint-engine.service")
}

func Unregister() error {
	path, err := serviceFilePath()
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); err != nil {
		return errors.New("engine: native service not registered")
	}
	if serviceOS() == "darwin" {
		if err := launchctlCmd("bootout", guiServiceTarget()); err != nil {
			return err
		}
	} else {
		if err := systemctlCmd("--user", "stop", "flint-engine.service"); err != nil {
			return err
		}
	}
	return os.Remove(path)
}

func nativeStatus() (ServiceStatus, error) {
	st := ServiceStatus{Mode: ModeNative}
	path, err := serviceFilePath()
	if err != nil {
		return st, err
	}
	if _, err := os.Stat(path); err != nil {
		return st, nil
	}
	st.Registered = true
	out, err := execVersion(currentBinaryPath())
	if err != nil {
		return st, nil
	}
	st.Running = true
	st.Version = versionFromOutput(out)
	if st.Version != NativePinVersion {
		st.Detail = fmt.Sprintf("running %s differs from pinned %s — re-run flint setup --engine native to update", st.Version, NativePinVersion)
	}
	return st, nil
}

func NewNativeService() NativeService {
	return NativeService{
		Status: nativeStatus,
		Start: func() error {
			if serviceOS() == "darwin" {
				return launchctlCmd("kickstart", guiServiceTarget())
			}
			return systemctlCmd("--user", "start", "flint-engine.service")
		},
		Stop: func() error {
			if serviceOS() == "darwin" {
				return launchctlCmd("kill", "TERM", guiServiceTarget())
			}
			return systemctlCmd("--user", "stop", "flint-engine.service")
		},
		Version: func() (string, error) {
			out, err := execVersion(currentBinaryPath())
			if err != nil {
				return "", err
			}
			return versionFromOutput(out), nil
		},
	}
}

func WriteEngineConfig(home string) error {
	dir := filepath.Join(home, "engine")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	cfg := "listener:\n  host: 127.0.0.1\n  port: 5434\n"
	return os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(cfg), 0o600)
}

func WaitForEngine(ctx context.Context, dsn string, attempts int, delay time.Duration) error {
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return err
	}
	addr := net.JoinHostPort(cfg.Host, fmt.Sprintf("%d", cfg.Port))
	for i := 0; i < attempts; i++ {
		conn, derr := net.DialTimeout("tcp", addr, 2*time.Second)
		if derr == nil {
			conn.Close()
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}
	return fmt.Errorf("engine: not reachable at %s after %d attempts", addr, attempts)
}


func TailEngineLogs(n int) (string, error) {
	_, errLog := logFilePaths()
	b, err := os.ReadFile(errLog)
	if err != nil {
		return "", err
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n"), nil
}
