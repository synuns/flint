package cli

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"
)

// v0.7 T76 — setup 보강: compose up --wait 인자 정합 · docker 미설치 안내 구분.

// TestSetupWaitArgs — 기본 composeUpFn은 composeRunFn에
// ["up","-d","--wait","--wait-timeout","60"]을 전달한다(스펙 §2 대기).
func TestSetupWaitArgs(t *testing.T) {
	var gotArgs []string
	prevUp, prevRun := composeUpFn, composeRunFn
	composeRunFn = func(ctx context.Context, args ...string) error {
		gotArgs = append([]string{}, args...)
		return nil
	}
	t.Cleanup(func() { composeUpFn, composeRunFn = prevUp, prevRun })

	if err := composeUpFn(context.Background()); err != nil {
		t.Fatalf("composeUpFn 오류: %v", err)
	}
	want := []string{"up", "-d", "--wait", "--wait-timeout", "60"}
	if len(gotArgs) != len(want) {
		t.Fatalf("인자=%v, want %v", gotArgs, want)
	}
	for i := range want {
		if gotArgs[i] != want[i] {
			t.Fatalf("인자[%d]=%q, want %q (전체 %v)", i, gotArgs[i], want[i], gotArgs)
		}
	}
}

// TestSetupDockerMissingHint — exec not found(docker 미설치)는 전용 안내로
// 구분, 그 외 실패(타임아웃 등)는 기존 경고 경로 유지.
func TestSetupDockerMissingHint(t *testing.T) {
	withFakeCompose(t)
	rec := withDockerMissing(t)
	line, code := RunSetup(context.Background(),
		func() error { return nil },
		func() error { return nil })
	if code != 0 {
		t.Fatalf("exit=%d, want 0(경고로 계속); out=%s", code, line)
	}
	m := decode(t, line)
	w, _ := m["warnings"].([]any)
	if len(w) != 1 {
		t.Fatalf("warnings=%v, want 1건", m["warnings"])
	}
	msg := fmt.Sprint(w[0])
	if !strings.Contains(msg, "docker 미설치") ||
		!strings.Contains(msg, "https://docs.docker.com/get-docker/") ||
		!strings.Contains(msg, "설치 후 재실행") {
		t.Fatalf("미설치 안내 문구 불일치: %q", msg)
	}
	if rec.upErr == nil {
		t.Fatal("테스트 설정 오류 — upErr 미주입")
	}
}

// withDockerMissing — composeUpFn이 exec not found를 반환하는 seam.
func withDockerMissing(t *testing.T) *composeRecorder {
	t.Helper()
	rec := &composeRecorder{}
	rec.upErr = &exec.Error{Name: "docker", Err: exec.ErrNotFound}
	prevUp := composeUpFn
	composeUpFn = func(ctx context.Context) error { return rec.upErr }
	t.Cleanup(func() { composeUpFn = prevUp })
	return rec
}
