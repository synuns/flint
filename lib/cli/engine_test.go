package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/engine"
	"github.com/synuns/flint/lib/wire"
)

func withEngineMode(t *testing.T, mode string) {
	t.Helper()
	withEngineConfigDir(t)
	if err := engine.SetMode(mode); err != nil {
		t.Fatal(err)
	}
}

func withContainerServiceRecorder(t *testing.T) *[]string {
	t.Helper()
	var calls []string
	engine.SetContainerService(engine.ContainerService{
		Status: func() (engine.ServiceStatus, error) {
			calls = append(calls, "status")
			return engine.ServiceStatus{Mode: engine.ModeManagedContainer, Running: true, Version: "0.1.0"}, nil
		},
		Start:   func() error { calls = append(calls, "start"); return nil },
		Stop:    func() error { calls = append(calls, "stop"); return nil },
		Version: func() (string, error) { return "0.1.0", nil },
	})
	t.Cleanup(func() { engine.SetContainerService(engine.ContainerService{}) })
	return &calls
}

func TestEngineCommandRegistered(t *testing.T) {
	if _, ok := wire.Lookup("engine"); !ok {
		t.Fatal("engine 커맨드 미등록")
	}
	found := false
	for _, name := range wire.UsageOrder() {
		if name == "engine" {
			found = true
		}
	}
	if !found {
		t.Fatal("usageOrder에 engine 없음")
	}
	if !strings.Contains(wire.HelpSummary(), "engine") {
		t.Fatal("HelpSummary에 engine 없음")
	}
}

func TestEngineDelegatesStatusStartStop(t *testing.T) {
	withEngineMode(t, engine.ModeManagedContainer)
	calls := withContainerServiceRecorder(t)
	for _, sub := range []string{"status", "start", "stop"} {
		var out bytes.Buffer
		if code := Run([]string{"engine", sub}, strings.NewReader(""), &out); code != 0 {
			t.Fatalf("engine %s exit=%d, want 0; out=%s", sub, code, out.String())
		}
	}
	joined := strings.Join(*calls, ",")
	for _, sub := range []string{"status", "start", "stop"} {
		if !strings.Contains(joined, sub) {
			t.Fatalf("위임 누락 %q — calls=%v", sub, *calls)
		}
	}
}

func TestEngineNoSubcommandUsageExit3(t *testing.T) {
	withEngineConfigDir(t)
	var out bytes.Buffer
	if code := Run([]string{"engine"}, strings.NewReader(""), &out); code != 3 {
		t.Fatalf("exit=%d, want 3; out=%s", code, out.String())
	}
	if !strings.Contains(out.String(), "usage") {
		t.Fatalf("usage 안내 없음: %s", out.String())
	}
}

func TestEngineUnknownSubcommandExit3(t *testing.T) {
	withEngineConfigDir(t)
	var out bytes.Buffer
	if code := Run([]string{"engine", "restart"}, strings.NewReader(""), &out); code != 3 {
		t.Fatalf("exit=%d, want 3; out=%s", code, out.String())
	}
}

func TestEngineStatusNotWiredAdvisory(t *testing.T) {
	withEngineMode(t, engine.ModeNative)
	engine.SetNativeService(engine.NativeService{})
	t.Cleanup(func() { engine.SetNativeService(engine.NativeService{}) })
	var out bytes.Buffer
	if code := Run([]string{"engine", "status"}, strings.NewReader(""), &out); code != 1 {
		t.Fatalf("exit=%d, want 1; out=%s", code, out.String())
	}
	if !strings.Contains(out.String(), "not wired") {
		t.Fatalf("ErrNotWired 안내 없음: %s", out.String())
	}
}

func TestEngineStatusJSONShape(t *testing.T) {
	withEngineMode(t, engine.ModeManagedContainer)
	withContainerServiceRecorder(t)
	var out bytes.Buffer
	if code := Run([]string{"engine", "status"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out.String())
	}
	var st engine.ServiceStatus
	if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &st); err != nil {
		t.Fatalf("status JSON 파싱 실패: %v — %s", err, out.String())
	}
	if st.Mode != engine.ModeManagedContainer || !st.Running {
		t.Fatalf("status=%+v", st)
	}
}

func TestSetupEngineModeIdempotent(t *testing.T) {
	withEngineConfigDir(t)
	prevD, prevS, prevB := engineNativeDownloadFn, engineNativeServiceFn, engineNativeBootstrapFn
	engineNativeDownloadFn = func(ctx context.Context) error { return nil }
	engineNativeServiceFn = func(ctx context.Context) error { return nil }
	engineNativeBootstrapFn = func(ctx context.Context) error { return nil }
	t.Cleanup(func() {
		engineNativeDownloadFn, engineNativeServiceFn, engineNativeBootstrapFn = prevD, prevS, prevB
	})
	var out bytes.Buffer
	if code := Run([]string{"setup", "--engine", "native"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("setup --engine native exit=%d, want 0; out=%s", code, out.String())
	}
	if got := engine.CurrentMode(); got != engine.ModeNative {
		t.Fatalf("CurrentMode=%q, want native", got)
	}
	out.Reset()
	if code := Run([]string{"setup", "--engine", "native"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("재실행 exit=%d, want 0 (멱등); out=%s", code, out.String())
	}
	out.Reset()
	if code := Run([]string{"setup", "--engine", "managed-container"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("setup --engine managed-container exit=%d, want 0; out=%s", code, out.String())
	}
	if got := engine.CurrentMode(); got != engine.ModeManagedContainer {
		t.Fatalf("왕복 후 CurrentMode=%q, want managed-container", got)
	}
}

func TestSetupEngineNativeProvisionSeam(t *testing.T) {
	withEngineConfigDir(t)
	var calls []string
	prevD, prevS, prevB := engineNativeDownloadFn, engineNativeServiceFn, engineNativeBootstrapFn
	engineNativeDownloadFn = func(ctx context.Context) error { calls = append(calls, "download"); return nil }
	engineNativeServiceFn = func(ctx context.Context) error { calls = append(calls, "service"); return nil }
	engineNativeBootstrapFn = func(ctx context.Context) error { calls = append(calls, "bootstrap"); return nil }
	t.Cleanup(func() {
		engineNativeDownloadFn, engineNativeServiceFn, engineNativeBootstrapFn = prevD, prevS, prevB
	})
	var out bytes.Buffer
	if code := Run([]string{"setup", "--engine", "native"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out.String())
	}
	joined := strings.Join(calls, ",")
	for _, want := range []string{"download", "service", "bootstrap"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("native seam 호출 누락 %q — calls=%v", want, calls)
		}
	}
}

func TestSetupEngineInvalidModeExit3(t *testing.T) {
	withEngineConfigDir(t)
	var out bytes.Buffer
	if code := Run([]string{"setup", "--engine", "bogus"}, strings.NewReader(""), &out); code != 3 {
		t.Fatalf("exit=%d, want 3; out=%s", code, out.String())
	}
}

func TestSetupEngineRequiresValue(t *testing.T) {
	withEngineConfigDir(t)
	var out bytes.Buffer
	if code := Run([]string{"setup", "--engine"}, strings.NewReader(""), &out); code != 3 {
		t.Fatalf("exit=%d, want 3; out=%s", code, out.String())
	}
}

func withComposePsOutput(t *testing.T, out string, err error) {
	t.Helper()
	prev := composePsFn
	composePsFn = func(ctx context.Context) ([]byte, error) { return []byte(out), err }
	t.Cleanup(func() { composePsFn = prev })
}

func TestEngineDefaultWiringContainerStatusV1311(t *testing.T) {
	wireEngineServices()
	withEngineMode(t, engine.ModeManagedContainer)
	ps := `{"Name":"flint-doltgres-1","State":"running","Image":"dolthub/doltgresql@sha256:demo","Labels":"com.docker.compose.project=flint"}` + "\n"
	withComposePsOutput(t, ps, nil)
	var out bytes.Buffer
	if code := Run([]string{"engine", "status"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out.String())
	}
	var st engine.ServiceStatus
	if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &st); err != nil {
		t.Fatalf("status JSON 파싱 실패: %v — %s", err, out.String())
	}
	if st.Mode != engine.ModeManagedContainer || !st.Running || !st.Registered {
		t.Fatalf("status=%+v, want managed-container running", st)
	}
	if st.Version != "dolthub/doltgresql@sha256:demo" {
		t.Fatalf("version=%q, want running container image", st.Version)
	}
}

func TestEngineDefaultWiringContainerStatusNotRunningV1311(t *testing.T) {
	wireEngineServices()
	withEngineMode(t, engine.ModeManagedContainer)
	withComposePsOutput(t, "", nil)
	var out bytes.Buffer
	if code := Run([]string{"engine", "status"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out.String())
	}
	var st engine.ServiceStatus
	if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &st); err != nil {
		t.Fatalf("status JSON 파싱 실패: %v — %s", err, out.String())
	}
	if st.Running || st.Registered {
		t.Fatalf("status=%+v, want no flint container", st)
	}
}

func TestEngineDefaultWiringContainerStatusIgnoresOtherProjectsV1311(t *testing.T) {
	wireEngineServices()
	withEngineMode(t, engine.ModeManagedContainer)
	ps := `{"Name":"other-db-1","State":"running","Image":"postgres:16","Labels":"com.docker.compose.project=other"}` + "\n"
	withComposePsOutput(t, ps, nil)
	var out bytes.Buffer
	if code := Run([]string{"engine", "status"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out.String())
	}
	var st engine.ServiceStatus
	if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &st); err != nil {
		t.Fatalf("status JSON 파싱 실패: %v — %s", err, out.String())
	}
	if st.Running || st.Registered {
		t.Fatalf("status=%+v, want other-project containers ignored", st)
	}
}

func TestEngineDefaultWiringContainerStatusDockerErrorV1311(t *testing.T) {
	wireEngineServices()
	withEngineMode(t, engine.ModeManagedContainer)
	withComposePsOutput(t, "", errors.New("docker ps boom"))
	var out bytes.Buffer
	if code := Run([]string{"engine", "status"}, strings.NewReader(""), &out); code != 1 {
		t.Fatalf("exit=%d, want 1; out=%s", code, out.String())
	}
	if !strings.Contains(out.String(), "docker ps boom") {
		t.Fatalf("docker 오류 미노출: %s", out.String())
	}
}

func TestEngineDefaultWiringContainerStartStopV1311(t *testing.T) {
	wireEngineServices()
	withEngineMode(t, engine.ModeManagedContainer)
	ups := 0
	var downs []bool
	prevUp, prevDown := composeUpFn, composeDownFn
	composeUpFn = func(ctx context.Context) error { ups++; return nil }
	composeDownFn = func(ctx context.Context, purge bool) error { downs = append(downs, purge); return nil }
	t.Cleanup(func() { composeUpFn, composeDownFn = prevUp, prevDown })
	var out bytes.Buffer
	if code := Run([]string{"engine", "start"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("start exit=%d, want 0; out=%s", code, out.String())
	}
	if ups != 1 {
		t.Fatalf("composeUpFn calls=%d, want 1", ups)
	}
	out.Reset()
	if code := Run([]string{"engine", "stop"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("stop exit=%d, want 0; out=%s", code, out.String())
	}
	if !reflect.DeepEqual(downs, []bool{false}) {
		t.Fatalf("composeDownFn purge args=%v, want [false]", downs)
	}
}

func TestEngineDefaultWiringNativeStatusV1311(t *testing.T) {
	wireEngineServices()
	withEngineMode(t, engine.ModeNative)
	t.Setenv("FLINT_HOME", t.TempDir())
	var out bytes.Buffer
	if code := Run([]string{"engine", "status"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out.String())
	}
	if strings.Contains(out.String(), "not wired") {
		t.Fatalf("native 서비스 미와이어: %s", out.String())
	}
	var st engine.ServiceStatus
	if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &st); err != nil {
		t.Fatalf("status JSON 파싱 실패: %v — %s", err, out.String())
	}
	if st.Mode != engine.ModeNative {
		t.Fatalf("mode=%q, want native", st.Mode)
	}
}
