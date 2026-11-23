package cli

import (
	"bytes"
	"context"
	"github.com/synuns/flint/lib/store"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNudgeShortDirectiveNormal(t *testing.T) {
	if nudgePendingLine != "flint: distillation pending — flint nudge" {
		t.Errorf("짧은 문구 = %q", nudgePendingLine)
	}
	if strings.Contains(nudgePendingLine, "flint write knowledge") {
		t.Error("명령문 잔존 — 최소화 위반")
	}
}

func TestNudgeMinimalAutoClears(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FLINT_CONFIG_DIR", dir)
	mf := MarkerFile()
	if err := os.MkdirAll(filepath.Dir(mf), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := WriteMarker("sess-1", "compact"); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	code := runNudge(context.Background(), fakeProfileStore(t, "minimal"), nil, strings.NewReader(""), &out)
	if code != 0 || out.Len() != 0 {
		t.Fatalf("출력 있음/exit=%d: %q", code, out.String())
	}
	if _, err := os.Stat(mf); !os.IsNotExist(err) {
		t.Error("마커 미소멸")
	}
}

func fakeProfileStore(t *testing.T, p string) Store {
	if os.Getenv("SUPERLITE_REAL") != "1" {
		orig := exposureProfileFn
		t.Cleanup(func() { exposureProfileFn = orig })
		exposureProfileFn = func(func(string) (string, error)) string { return p }
	}
	return &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if len(args) > 0 && args[0] == "exposure_profile" {
			return [][]any{{p}}, nil
		}
		return nil, nil
	}}
}

func TestNudgeStoreDownQuiet(t *testing.T) {
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	prev := newStore
	newStore = func(ctx context.Context, dsn string) (Store, error) {
		return nil, store.ErrServerDown
	}
	t.Cleanup(func() { newStore = prev })

	var out bytes.Buffer
	code := Run([]string{"nudge"}, strings.NewReader(`{"hook_event_name":"Stop"}`), &out)
	if code != 2 {
		t.Fatalf("exit=%d, want 2(server_down)", code)
	}
	if out.String() != "" {
		t.Fatalf("stdout=%q, want 빈 문자열(에러 JSON 누출)", out.String())
	}
}

func TestNudgeRecallMarkerOneShot(t *testing.T) {
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	if err := WriteTypedMarker(markerKindRecall, "s1", nudgeReasonRecall); err != nil {
		t.Fatalf("WriteTypedMarker err = %v", err)
	}
	out, code := nudgeRun(nil, `{"hook_event_name":"SessionStart","session_id":"s2"}`)
	if code != 0 {
		t.Fatalf("exit=%d, want 0", code)
	}
	if strings.TrimSpace(out) != nudgeRecallStartLine {
		t.Fatalf("out=%q, want recall 문구", out)
	}
	if _, err := os.Stat(RecallMarkerFile()); !os.IsNotExist(err) {
		t.Fatalf("recall 마커 미소진: %v", err)
	}

	out2, _ := nudgeRun(nil, `{"hook_event_name":"SessionStart","session_id":"s2"}`)
	if strings.TrimSpace(out2) != "" {
		t.Fatalf("소진 후 out=%q, want 무출력", out2)
	}
}

func TestNudgeCompactMarkerUnchanged(t *testing.T) {
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	if err := WriteMarker("s1", "compact"); err != nil {
		t.Fatalf("WriteMarker err = %v", err)
	}
	var out bytes.Buffer
	code := runNudge(context.Background(), nil, nil,
		strings.NewReader(`{"hook_event_name":"SessionStart","session_id":"s2"}`), &out)
	if code != 0 {
		t.Fatalf("exit=%d", code)
	}
	if !strings.Contains(out.String(), nudgePendingLine) {
		t.Fatalf("out=%q, want 기존 증류 안내", out.String())
	}
	if _, err := os.Stat(MarkerFile()); err != nil {
		t.Fatalf("compact 마커가 소진됨(불변 계약 위반): %v", err)
	}
}

func TestNudgeRecallSilentOnStop(t *testing.T) {
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	if err := WriteTypedMarker(markerKindRecall, "s1", nudgeReasonRecall); err != nil {
		t.Fatalf("WriteTypedMarker err = %v", err)
	}
	out, code := nudgeRun(nil, `{"hook_event_name":"Stop","session_id":"s1","stop_hook_active":false}`)
	if code != 0 {
		t.Fatalf("exit=%d, want 0", code)
	}
	if strings.TrimSpace(out) != "" {
		t.Fatalf("out=%q, want 무출력(recall은 SessionStart 전용)", out)
	}
	if _, err := os.Stat(RecallMarkerFile()); err != nil {
		t.Fatalf("recall 마커가 Stop에서 소진됨: %v", err)
	}
}

func TestNudgeRecallDoesNotClobberDistill(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FLINT_CONFIG_DIR", dir)
	if err := WriteMarker("s1", "compact"); err != nil {
		t.Fatalf("WriteMarker err = %v", err)
	}
	if err := WriteTypedMarker(markerKindRecall, "s1", nudgeReasonRecall); err != nil {
		t.Fatalf("WriteTypedMarker err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "pending-distill.json")); err != nil {
		t.Fatalf("증류 마커가 recall 기록에 덮어써짐: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "recall-hint.json")); err != nil {
		t.Fatalf("recall 마커 미기록: %v", err)
	}
}

func TestNudgeSessionStartBothMarkers(t *testing.T) {
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	if err := WriteMarker("s1", "compact"); err != nil {
		t.Fatalf("WriteMarker err = %v", err)
	}
	if err := WriteTypedMarker(markerKindRecall, "s1", nudgeReasonRecall); err != nil {
		t.Fatalf("WriteTypedMarker err = %v", err)
	}
	out, code := nudgeRun(nil, `{"hook_event_name":"SessionStart","session_id":"s2"}`)
	if code != 0 {
		t.Fatalf("exit=%d", code)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("출력 %d줄, want 2: %q", len(lines), out)
	}
	if lines[0] != nudgeRecallStartLine {
		t.Fatalf("첫 줄=%q, want recall 문구", lines[0])
	}
	if !strings.Contains(lines[1], nudgePendingLine) {
		t.Fatalf("둘째 줄=%q, want 증류 안내", lines[1])
	}
	if _, err := os.Stat(RecallMarkerFile()); !os.IsNotExist(err) {
		t.Fatalf("recall 마커 미소진")
	}
	if _, err := os.Stat(MarkerFile()); err != nil {
		t.Fatalf("증류 마커가 소진됨(내구 계약 위반): %v", err)
	}
}

func TestNudgeLegacyRecallInDistillFile(t *testing.T) {
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	if err := WriteMarker("s1", nudgeReasonRecall); err != nil {
		t.Fatalf("WriteMarker err = %v", err)
	}
	out, code := nudgeRun(nil, `{"hook_event_name":"SessionStart","session_id":"s2"}`)
	if code != 0 {
		t.Fatalf("exit=%d", code)
	}
	if strings.TrimSpace(out) != nudgeRecallStartLine {
		t.Fatalf("out=%q, want recall 문구(마이그레이션)", out)
	}
	if _, err := os.Stat(MarkerFile()); !os.IsNotExist(err) {
		t.Fatalf("레거시 recall 마커 미소진")
	}
}

func TestNudgeReadHintConstant(t *testing.T) {
	if !strings.Contains(nudgeReadHint, "flint read") {
		t.Errorf("read 힌트 문구 이상: %q", nudgeReadHint)
	}
}

func TestNudgeStopDirectivePreserved(t *testing.T) {
	if !strings.Contains(nudgePendingLine, "distillation pending") {
		t.Error("Stop 짧은 directive 훼손")
	}
	if !strings.Contains(nudgePendingLine, "flint nudge") {
		t.Error("SessionStart 라인 훼손")
	}
}
