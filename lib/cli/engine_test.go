package cli

import (
	"bytes"
	"context"
	"encoding/json"
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
