package engine

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type serviceRecorder struct {
	home       string
	launchArgs [][]string
	sysArgs    [][]string
}

func setupService(t *testing.T, osName, versionOutput string, versionErr error) *serviceRecorder {
	t.Helper()
	rec := &serviceRecorder{home: t.TempDir()}
	t.Setenv("FLINT_HOME", rec.home)
	savedOS := serviceOS
	serviceOS = func() string { return osName }
	savedHome := userHomeDir
	userHomeDir = func() (string, error) { return rec.home, nil }
	savedL := launchctlCmd
	launchctlCmd = func(args ...string) error {
		rec.launchArgs = append(rec.launchArgs, args)
		return nil
	}
	savedS := systemctlCmd
	systemctlCmd = func(args ...string) error {
		rec.sysArgs = append(rec.sysArgs, args)
		return nil
	}
	savedV := execVersion
	execVersion = func(string) (string, error) { return versionOutput, versionErr }
	t.Cleanup(func() {
		serviceOS = savedOS
		userHomeDir = savedHome
		launchctlCmd = savedL
		systemctlCmd = savedS
		execVersion = savedV
	})
	return rec
}

func fakeCurrentBinary(t *testing.T, home string) string {
	t.Helper()
	dir := filepath.Join(home, "engine", "current")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "doltgres")
	if err := os.WriteFile(bin, []byte("fake"), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

func plistPath(home string) string {
	return filepath.Join(home, "Library", "LaunchAgents", "dev.flint.engine.plist")
}

func unitPath(home string) string {
	return filepath.Join(home, ".config", "systemd", "user", "flint-engine.service")
}

func assertNoSecrets(t *testing.T, body string) {
	t.Helper()
	for _, s := range []string{"flint-dev", "password", "postgresql://"} {
		if strings.Contains(body, s) {
			t.Fatalf("service file leaks secret %q", s)
		}
	}
}

func assertPerm0600(t *testing.T, path string) {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("%s perm = %o, want 600", path, fi.Mode().Perm())
	}
}

func TestRegisterDarwinPlist(t *testing.T) {
	rec := setupService(t, "darwin", "", nil)
	bin := fakeCurrentBinary(t, rec.home)

	if err := Register(); err != nil {
		t.Fatalf("Register: %v", err)
	}
	path := plistPath(rec.home)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("plist missing: %v", err)
	}
	assertPerm0600(t, path)
	assertNoSecrets(t, string(body))
	for _, want := range []string{
		"<string>dev.flint.engine</string>",
		"<string>" + bin + "</string>",
		"<key>KeepAlive</key>",
		"<string>" + filepath.Join(rec.home, "engine", "logs", "engine.out.log") + "</string>",
		"<string>" + filepath.Join(rec.home, "engine", "logs", "engine.err.log") + "</string>",
	} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("plist missing %q:\n%s", want, body)
		}
	}
	if len(rec.launchArgs) != 1 {
		t.Fatalf("launchctl calls = %d, want 1: %v", len(rec.launchArgs), rec.launchArgs)
	}
	call := rec.launchArgs[0]
	if call[0] != "bootstrap" || !strings.HasPrefix(call[1], "gui/") || call[2] != path {
		t.Fatalf("launchctl args = %v", call)
	}
	if len(rec.sysArgs) != 0 {
		t.Fatalf("systemctl must not run on darwin: %v", rec.sysArgs)
	}
}

func TestRegisterLinuxUnit(t *testing.T) {
	rec := setupService(t, "linux", "", nil)
	bin := fakeCurrentBinary(t, rec.home)

	if err := Register(); err != nil {
		t.Fatalf("Register: %v", err)
	}
	path := unitPath(rec.home)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("unit missing: %v", err)
	}
	assertPerm0600(t, path)
	assertNoSecrets(t, string(body))
	for _, want := range []string{
		"ExecStart=" + bin,
		"Restart=always",
		"StandardOutput=append:" + filepath.Join(rec.home, "engine", "logs", "engine.out.log"),
		"StandardError=append:" + filepath.Join(rec.home, "engine", "logs", "engine.err.log"),
	} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("unit missing %q:\n%s", want, body)
		}
	}
	if len(rec.sysArgs) != 2 {
		t.Fatalf("systemctl calls = %d, want 2: %v", len(rec.sysArgs), rec.sysArgs)
	}
	if rec.sysArgs[0][0] != "--user" || rec.sysArgs[0][1] != "daemon-reload" {
		t.Fatalf("daemon-reload args = %v", rec.sysArgs[0])
	}
	if rec.sysArgs[1][2] != "flint-engine.service" || rec.sysArgs[1][1] != "enable" {
		t.Fatalf("enable args = %v", rec.sysArgs[1])
	}
	if len(rec.launchArgs) != 0 {
		t.Fatalf("launchctl must not run on linux: %v", rec.launchArgs)
	}
}

func TestRegisterCreatesLogDir(t *testing.T) {
	rec := setupService(t, "darwin", "", nil)
	fakeCurrentBinary(t, rec.home)
	if err := Register(); err != nil {
		t.Fatalf("Register: %v", err)
	}
	fi, err := os.Stat(filepath.Join(rec.home, "engine", "logs"))
	if err != nil {
		t.Fatalf("log dir missing: %v", err)
	}
	if !fi.IsDir() {
		t.Fatal("logs is not a directory")
	}
}

func TestRegisterRequiresBinary(t *testing.T) {
	rec := setupService(t, "darwin", "", nil)
	if err := Register(); err == nil {
		t.Fatal("Register succeeded without binary, want error")
	}
	if _, err := os.Stat(plistPath(rec.home)); !os.IsNotExist(err) {
		t.Fatalf("plist must not exist after failed register: %v", err)
	}
	if len(rec.launchArgs) != 0 {
		t.Fatalf("launchctl must not run on failure: %v", rec.launchArgs)
	}
}

func TestRegisterUnsupportedOS(t *testing.T) {
	rec := setupService(t, "windows", "", nil)
	fakeCurrentBinary(t, rec.home)
	err := Register()
	if !errors.Is(err, ErrUnsupportedPlatform) {
		t.Fatalf("err = %v, want ErrUnsupportedPlatform", err)
	}
}

func TestStatusNotRegistered(t *testing.T) {
	setupService(t, "darwin", "", nil)
	st, err := nativeStatus()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.Registered || st.Running || st.Version != "" {
		t.Fatalf("status = %+v, want all false/empty", st)
	}
}

func TestStatusRegisteredRunning(t *testing.T) {
	rec := setupService(t, "darwin", "Doltgres version "+NativePinVersion+"\n", nil)
	fakeCurrentBinary(t, rec.home)
	if err := Register(); err != nil {
		t.Fatal(err)
	}
	st, err := nativeStatus()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !st.Registered || !st.Running {
		t.Fatalf("status = %+v, want registered+running", st)
	}
	if st.Version != NativePinVersion {
		t.Fatalf("version = %q, want %q", st.Version, NativePinVersion)
	}
	if strings.Contains(st.Detail, "differs") {
		t.Fatalf("pin-matched version must not report drift: %q", st.Detail)
	}
}

func TestStatusRegisteredNotRunning(t *testing.T) {
	rec := setupService(t, "darwin", "", errors.New("ping failed"))
	fakeCurrentBinary(t, rec.home)
	if err := Register(); err != nil {
		t.Fatal(err)
	}
	st, err := nativeStatus()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !st.Registered || st.Running {
		t.Fatalf("status = %+v, want registered but not running", st)
	}
}

func TestStatusVersionDriftAgainstPin(t *testing.T) {
	rec := setupService(t, "linux", "Doltgres version 0.9.9\n", nil)
	fakeCurrentBinary(t, rec.home)
	if err := Register(); err != nil {
		t.Fatal(err)
	}
	st, err := nativeStatus()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.Version != "0.9.9" {
		t.Fatalf("version = %q, want 0.9.9", st.Version)
	}
	if !strings.Contains(st.Detail, NativePinVersion) {
		t.Fatalf("detail = %q, want pin comparison", st.Detail)
	}
}

func TestUnregisterDarwin(t *testing.T) {
	rec := setupService(t, "darwin", "", nil)
	fakeCurrentBinary(t, rec.home)
	if err := Register(); err != nil {
		t.Fatal(err)
	}
	if err := Unregister(); err != nil {
		t.Fatalf("Unregister: %v", err)
	}
	if _, err := os.Stat(plistPath(rec.home)); !os.IsNotExist(err) {
		t.Fatalf("plist not removed: %v", err)
	}
	if len(rec.launchArgs) != 2 {
		t.Fatalf("launchctl calls = %d, want 2", len(rec.launchArgs))
	}
	boot := rec.launchArgs[1]
	if boot[0] != "bootout" || boot[1] != fmt.Sprintf("gui/%d/dev.flint.engine", os.Getuid()) {
		t.Fatalf("bootout args = %v", boot)
	}
}

func TestUnregisterLinux(t *testing.T) {
	rec := setupService(t, "linux", "", nil)
	fakeCurrentBinary(t, rec.home)
	if err := Register(); err != nil {
		t.Fatal(err)
	}
	if err := Unregister(); err != nil {
		t.Fatalf("Unregister: %v", err)
	}
	if _, err := os.Stat(unitPath(rec.home)); !os.IsNotExist(err) {
		t.Fatalf("unit not removed: %v", err)
	}
	if len(rec.sysArgs) != 3 {
		t.Fatalf("systemctl calls = %d, want 3: %v", len(rec.sysArgs), rec.sysArgs)
	}
	stop := rec.sysArgs[2]
	if stop[0] != "--user" || stop[1] != "stop" || stop[2] != "flint-engine.service" {
		t.Fatalf("stop args = %v", stop)
	}
}

func TestUnregisterNotRegistered(t *testing.T) {
	rec := setupService(t, "darwin", "", nil)
	if err := Unregister(); err == nil {
		t.Fatal("Unregister succeeded without registration, want error")
	}
	if len(rec.launchArgs) != 0 {
		t.Fatalf("launchctl must not run: %v", rec.launchArgs)
	}
}

func TestServiceManagerMissingGuidance(t *testing.T) {
	err := runManagerCmd("flint-no-such-manager-xyz")
	if err == nil {
		t.Fatal("runManagerCmd succeeded for missing binary, want error")
	}
	for _, want := range []string{"flint-no-such-manager-xyz", "not found"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q missing guidance %q", err.Error(), want)
		}
	}
}

func TestNativeServiceStartStop(t *testing.T) {
	rec := setupService(t, "darwin", "", nil)
	fakeCurrentBinary(t, rec.home)
	if err := Register(); err != nil {
		t.Fatal(err)
	}
	svc := NewNativeService()
	if err := svc.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	last := rec.launchArgs[len(rec.launchArgs)-1]
	if last[0] != "kickstart" || last[1] != fmt.Sprintf("gui/%d/dev.flint.engine", os.Getuid()) {
		t.Fatalf("kickstart args = %v", last)
	}
	if err := svc.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	last = rec.launchArgs[len(rec.launchArgs)-1]
	if last[0] != "kill" || last[2] != fmt.Sprintf("gui/%d/dev.flint.engine", os.Getuid()) {
		t.Fatalf("kill args = %v", last)
	}
	v, err := svc.Version()
	if err != nil || v != "" {
		t.Fatalf("Version = %q, %v", v, err)
	}
}

func TestNativeServiceLinuxStartStop(t *testing.T) {
	rec := setupService(t, "linux", "", nil)
	fakeCurrentBinary(t, rec.home)
	if err := Register(); err != nil {
		t.Fatal(err)
	}
	svc := NewNativeService()
	if err := svc.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	last := rec.sysArgs[len(rec.sysArgs)-1]
	if last[1] != "start" || last[2] != "flint-engine.service" {
		t.Fatalf("start args = %v", last)
	}
	if err := svc.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	last = rec.sysArgs[len(rec.sysArgs)-1]
	if last[1] != "stop" {
		t.Fatalf("stop args = %v", last)
	}
}

func TestNativeServiceWiredIntoEngineStatus(t *testing.T) {
	setupService(t, "darwin", "Doltgres version "+NativePinVersion+"\n", nil)
	withConfigDir(t, `{"engine":{"mode":"native"}}`)
	resetSeams(t)
	SetNativeService(NewNativeService())
	st, err := Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.Mode != ModeNative || st.Registered {
		t.Fatalf("status = %+v, want native mode unregistered", st)
	}
}
