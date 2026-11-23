package cli

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"
)

func TestSetupWaitArgs(t *testing.T) {
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	withDockerPS(t, "", nil)
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
	if len(gotArgs) < 2 || gotArgs[0] != "-f" {
		t.Fatalf("인자=%v, want -f <path> 머리", gotArgs)
	}
	rest := gotArgs[2:]
	want := []string{"--project-name", "flint", "up", "-d", "--wait", "--wait-timeout", "60"}
	if len(rest) != len(want) {
		t.Fatalf("인자=%v, want -f <path> %v", gotArgs, want)
	}
	for i := range want {
		if rest[i] != want[i] {
			t.Fatalf("인자[%d]=%q, want %q (전체 %v)", i+2, rest[i], want[i], gotArgs)
		}
	}
}

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
	if !strings.Contains(msg, "docker not installed") ||
		!strings.Contains(msg, "https://docs.docker.com/get-docker/") ||
		!strings.Contains(msg, "re-run") {
		t.Fatalf("docker-missing hint mismatch: %q", msg)
	}
	if rec.upErr == nil {
		t.Fatal("테스트 설정 오류 — upErr 미주입")
	}
}

func withDockerMissing(t *testing.T) *composeRecorder {
	t.Helper()
	rec := &composeRecorder{}
	rec.upErr = &exec.Error{Name: "docker", Err: exec.ErrNotFound}
	prevUp := composeUpFn
	composeUpFn = func(ctx context.Context) error { return rec.upErr }
	t.Cleanup(func() { composeUpFn = prevUp })
	return rec
}
