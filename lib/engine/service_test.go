package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
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
	launchctlCmd = func(ctx context.Context, args ...string) error {
		rec.launchArgs = append(rec.launchArgs, args)
		return nil
	}
	savedS := systemctlCmd
	systemctlCmd = func(ctx context.Context, args ...string) error {
		rec.sysArgs = append(rec.sysArgs, args)
		return nil
	}
	savedLQ := launchctlQuery
	launchctlQuery = func(ctx context.Context, args ...string) (string, error) { return "", nil }
	savedSQ := systemctlQuery
	systemctlQuery = func(ctx context.Context, args ...string) (string, error) { return "", nil }
	savedState := serviceStateFn
	serviceStateFn = func() (string, error) {
		if versionErr != nil {
			return "", versionErr
		}
		if versionOutput != "" {
			return "running", nil
		}
		return "stopped", nil
	}
	savedV := execVersion
	execVersion = func(string) (string, error) { return versionOutput, versionErr }
	t.Cleanup(func() {
		serviceOS = savedOS
		userHomeDir = savedHome
		launchctlCmd = savedL
		systemctlCmd = savedS
		launchctlQuery = savedLQ
		systemctlQuery = savedSQ
		serviceStateFn = savedState
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
	if len(rec.launchArgs) != 2 {
		t.Fatalf("launchctl calls = %d, want 2: %v", len(rec.launchArgs), rec.launchArgs)
	}
	boot := rec.launchArgs[0]
	if boot[0] != "bootout" || boot[1] != fmt.Sprintf("gui/%d/dev.flint.engine", os.Getuid()) {
		t.Fatalf("bootout args = %v", boot)
	}
	call := rec.launchArgs[1]
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
	if len(rec.launchArgs) != 3 {
		t.Fatalf("launchctl calls = %d, want 3", len(rec.launchArgs))
	}
	boot := rec.launchArgs[2]
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
	err := runManagerCmd(context.Background(), "flint-no-such-manager-xyz")
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
	if last[0] != "bootout" || last[1] != fmt.Sprintf("gui/%d/dev.flint.engine", os.Getuid()) {
		t.Fatalf("stop args = %v, want bootout of gui target", last)
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

func TestRegisterDarwinIdempotentV1311(t *testing.T) {
	rec := setupService(t, "darwin", "", nil)
	fakeCurrentBinary(t, rec.home)
	if err := Register(); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	if err := Register(); err != nil {
		t.Fatalf("re-register must be idempotent: %v", err)
	}
	if len(rec.launchArgs) != 4 {
		t.Fatalf("launchctl calls = %d, want 4: %v", len(rec.launchArgs), rec.launchArgs)
	}
	for i := 0; i < 2; i++ {
		boot, strap := rec.launchArgs[2*i], rec.launchArgs[2*i+1]
		if boot[0] != "bootout" || boot[1] != fmt.Sprintf("gui/%d/dev.flint.engine", os.Getuid()) {
			t.Fatalf("round %d bootout args = %v", i, boot)
		}
		if strap[0] != "bootstrap" || strap[2] != plistPath(rec.home) {
			t.Fatalf("round %d bootstrap args = %v", i, strap)
		}
	}
}

func TestRegisterDarwinToleratesBootoutFailureV1311(t *testing.T) {
	rec := setupService(t, "darwin", "", nil)
	fakeCurrentBinary(t, rec.home)
	saved := launchctlCmd
	launchctlCmd = func(ctx context.Context, args ...string) error {
		if args[0] == "bootout" {
			return errors.New("bootout boom — service not loaded")
		}
		rec.launchArgs = append(rec.launchArgs, args)
		return nil
	}
	t.Cleanup(func() { launchctlCmd = saved })
	if err := Register(); err != nil {
		t.Fatalf("Register must tolerate bootout of unloaded service: %v", err)
	}
	if len(rec.launchArgs) != 1 || rec.launchArgs[0][0] != "bootstrap" {
		t.Fatalf("bootstrap must still run: %v", rec.launchArgs)
	}
}

func TestNativeServiceDarwinStopBootsOutNotKillV1311(t *testing.T) {
	rec := setupService(t, "darwin", "", nil)
	fakeCurrentBinary(t, rec.home)
	if err := Register(); err != nil {
		t.Fatal(err)
	}
	svc := NewNativeService()
	if err := svc.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	last := rec.launchArgs[len(rec.launchArgs)-1]
	if last[0] != "bootout" || last[1] != fmt.Sprintf("gui/%d/dev.flint.engine", os.Getuid()) {
		t.Fatalf("stop args = %v, want bootout of gui target", last)
	}
	for _, call := range rec.launchArgs {
		if call[0] == "kill" {
			t.Fatalf("kill must not be used with KeepAlive (restarts immediately): %v", call)
		}
	}
}

func TestNativeServiceDarwinStartAfterStopReBootstrapsV1311(t *testing.T) {
	rec := setupService(t, "darwin", "", nil)
	fakeCurrentBinary(t, rec.home)
	if err := Register(); err != nil {
		t.Fatal(err)
	}
	svc := NewNativeService()
	if err := svc.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if err := svc.Start(); err != nil {
		t.Fatalf("start after stop must reload the service: %v", err)
	}
	if len(rec.launchArgs) < 3 {
		t.Fatalf("launchctl calls = %v", rec.launchArgs)
	}
	tail := rec.launchArgs[len(rec.launchArgs)-3:]
	if tail[0][0] != "bootout" || tail[1][0] != "bootstrap" || tail[2][0] != "kickstart" {
		t.Fatalf("start-after-stop = %v, want bootout+bootstrap+kickstart", tail)
	}
}

func TestNativeStatusRunningDecoupledFromVersionProbeV1311(t *testing.T) {
	rec := setupService(t, "darwin", "", errors.New("probe failed"))
	fakeCurrentBinary(t, rec.home)
	if err := Register(); err != nil {
		t.Fatal(err)
	}
	serviceStateFn = func() (string, error) { return "running", nil }
	st, err := nativeStatus()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !st.Registered || !st.Running {
		t.Fatalf("status = %+v, want registered+running from service state, not -version exec", st)
	}
	if st.Version != "" {
		t.Fatalf("version = %q, want empty when probe fails", st.Version)
	}
}

func TestNativeStatusStoppedDespiteVersionProbeV1311(t *testing.T) {
	rec := setupService(t, "darwin", "Doltgres version "+NativePinVersion+"\n", nil)
	fakeCurrentBinary(t, rec.home)
	if err := Register(); err != nil {
		t.Fatal(err)
	}
	serviceStateFn = func() (string, error) { return "stopped", nil }
	st, err := nativeStatus()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !st.Registered || st.Running {
		t.Fatalf("status = %+v, want registered but not running when manager says stopped", st)
	}
}

func TestServiceStateDefaultDarwinLaunchctlPrintV1311(t *testing.T) {
	setupService(t, "darwin", "", nil)
	var got []string
	launchctlQuery = func(ctx context.Context, args ...string) (string, error) {
		got = args
		return "\tstate = running\n", nil
	}
	serviceStateFn = serviceStateDefault
	state, err := serviceStateDefault()
	if err != nil {
		t.Fatalf("serviceStateDefault: %v", err)
	}
	if state != "running" {
		t.Fatalf("state = %q, want running", state)
	}
	want := []string{"print", fmt.Sprintf("gui/%d/dev.flint.engine", os.Getuid())}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("launchctl args = %v, want %v", got, want)
	}
	launchctlQuery = func(ctx context.Context, args ...string) (string, error) { return "\tstate = waiting\n", nil }
	serviceStateFn = serviceStateDefault
	if state, err = serviceStateDefault(); err != nil || state != "stopped" {
		t.Fatalf("state = %q, %v — want stopped without running line", state, err)
	}
}

func TestServiceStateDefaultLinuxSystemctlIsActiveV1311(t *testing.T) {
	setupService(t, "linux", "", nil)
	var got []string
	systemctlQuery = func(ctx context.Context, args ...string) (string, error) {
		got = args
		return "active\n", nil
	}
	serviceStateFn = serviceStateDefault
	state, err := serviceStateDefault()
	if err != nil {
		t.Fatalf("serviceStateDefault: %v", err)
	}
	if state != "active" {
		t.Fatalf("state = %q, want active", state)
	}
	want := []string{"--user", "is-active", "flint-engine.service"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("systemctl args = %v, want %v", got, want)
	}
}

func TestRunManagerOutputCancelledCtxV1320(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	if _, err := runManagerOutput(ctx, "echo", "hi"); err == nil {
		t.Fatal("취소 ctx에서 runManagerOutput 성공 — ctx 전파 실패")
	}
	if el := time.Since(start); el > time.Second {
		t.Fatalf("종료까지 %s — 취소 ctx는 즉시 에러여야 함", el)
	}
}

func TestRunManagerOutputCtxDeadlinePropagatesV1320(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := runManagerOutput(ctx, "sleep", "10"); err == nil {
		t.Fatal("부모 데드라인 내 미종료 명령 성공 — CommandContext 미적용")
	}
	if el := time.Since(start); el > 3*time.Second {
		t.Fatalf("종료까지 %s — ctx 데드라인이 exec에 전파되지 않음", el)
	}
}
