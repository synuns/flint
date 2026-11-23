package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/store"
)

// composeRecorder — swaps and records the composeUpFn/composeDownFn seams (pure phase test, no exec).
type composeRecorder struct {
	upCalls int
	upErr   error

	downCalls int
	downErr   error
	downPurge bool
}

func withFakeCompose(t *testing.T) *composeRecorder {
	t.Helper()
	rec := &composeRecorder{}
	prevUp, prevDown := composeUpFn, composeDownFn
	composeUpFn = func(ctx context.Context) error {
		rec.upCalls++
		return rec.upErr
	}
	composeDownFn = func(ctx context.Context, purge bool) error {
		rec.downCalls++
		rec.downPurge = purge
		return rec.downErr
	}
	t.Cleanup(func() { composeUpFn, composeDownFn = prevUp, prevDown })
	return rec
}

// TestSetupSteps — the fixed 4-step order ((1)compose (2)migrate (3)seed (4)plugins).
func TestSetupSteps(t *testing.T) {
	steps := SetupSteps()
	if len(steps) != 4 {
		t.Fatalf("단계 수=%d, want 4: %v", len(steps), steps)
	}
	if !strings.Contains(steps[0], "docker compose up -d") {
		t.Fatalf("1단계=%q, want docker compose up -d", steps[0])
	}
	if !strings.Contains(steps[1], "migrate") {
		t.Fatalf("2단계=%q, want migrate", steps[1])
	}
}

// TestSetupAllGreen — fake ping/migrate success -> one-line JSON summary, exit 0.
func TestSetupAllGreen(t *testing.T) {
	rec := withFakeCompose(t)
	migrateCalled := false
	line, code := RunSetup(context.Background(),
		func() error { return nil },
		func() error { migrateCalled = true; return nil })
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, line)
	}
	if rec.upCalls != 1 || !migrateCalled {
		t.Fatalf("compose=%d회 migrate=%v, want 1/true", rec.upCalls, migrateCalled)
	}
	if strings.Count(strings.TrimSpace(line), "\n") != 0 {
		t.Fatalf("요약은 한 줄 JSON이어야 한다: %q", line)
	}
	m := decode(t, line)
	if m["status"] != "ok" {
		t.Fatalf("status=%v, want ok; out=%s", m["status"], line)
	}
	nexts, _ := m["next"].([]any)
	found := false
	for _, n := range nexts {
		if strings.Contains(fmt.Sprint(n), "flint seed --demo") {
			found = true
		}
	}
	if !found {
		t.Fatalf("seed 안내(next) 없음: %v", m["next"])
	}
}

// TestSetupPingFailKeepsGuidance — even on ping failure the next-step guidance is kept (protocol).
func TestSetupPingFailKeepsGuidance(t *testing.T) {
	withFakeCompose(t)
	migrateCalled := false
	line, code := RunSetup(context.Background(),
		func() error { return context.DeadlineExceeded },
		func() error { migrateCalled = true; return nil })
	if code != 1 {
		t.Fatalf("exit=%d, want 1(degraded); out=%s", code, line)
	}
	if migrateCalled {
		t.Fatal("ping 실패 시 migrate를 건너뛰어야 한다")
	}
	m := decode(t, line)
	if m["status"] != "degraded" {
		t.Fatalf("status=%v, want degraded", m["status"])
	}
	if w, _ := m["warnings"].([]any); len(w) == 0 {
		t.Fatalf("warnings 없음: %v", m)
	}
	if nexts, _ := m["next"].([]any); len(nexts) == 0 {
		t.Fatal("ping 실패로 다음 단계 안내가 유실됨")
	}
}

// TestSetupComposeFailNotFatal — docker not installed etc. is a warning (continue after guidance, exit 0).
func TestSetupComposeFailNotFatal(t *testing.T) {
	rec := withFakeCompose(t)
	rec.upErr = context.Canceled
	line, code := RunSetup(context.Background(),
		func() error { return nil },
		func() error { return nil })
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, line)
	}
	m := decode(t, line)
	if w, _ := m["warnings"].([]any); len(w) != 1 {
		t.Fatalf("warnings=%v, want 1건(compose 안내)", m["warnings"])
	}
}

// TestSetupDry — --dry prints the steps only; no exec or store operations.
func TestSetupDry(t *testing.T) {
	f := &fakeStore{}
	withFake(t, f)
	rec := withFakeCompose(t)
	out, code := mustRun(t, []string{"setup", "--dry"}, "")
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	m := decode(t, out)
	if m["status"] != "dry" {
		t.Fatalf("status=%v, want dry; out=%s", m["status"], out)
	}
	steps, _ := m["steps"].([]any)
	if len(steps) != len(SetupSteps()) {
		t.Fatalf("steps=%v, want %v", steps, SetupSteps())
	}
	if rec.upCalls != 0 || len(f.queries) != 0 {
		t.Fatalf("dry에서 exec/쿼리 발생: up=%d queries=%d", rec.upCalls, len(f.queries))
	}
}

// TestSetupUnknownFlag — an unknown flag is a one-line JSON error + invalid-arg
// exit 3.
func TestSetupUnknownFlag(t *testing.T) {
	withFake(t, &fakeStore{})
	out, code := mustRun(t, []string{"setup", "--bogus"}, "")
	if code != 3 {
		t.Fatalf("exit=%d, want 3; out=%s", code, out)
	}
	decode(t, out)
}

// TestSetupRegistered — verifies the init() Register.
func TestSetupRegistered(t *testing.T) {
	if _, ok := Lookup("setup"); !ok {
		t.Fatal("Lookup(\"setup\") 실패 — 미등록 커맨드")
	}
	if _, ok := Lookup("teardown"); !ok {
		t.Fatal("Lookup(\"teardown\") 실패 — 미등록 커맨드")
	}
}

// TestTeardownDown — no args is a dry-run (plan notice only, no exec,
// exit 0). Only --yes executes the stop (see TestTeardownYes below).
func TestTeardownDown(t *testing.T) {
	withFake(t, &fakeStore{})
	rec := withFakeCompose(t)
	out, code := mustRun(t, []string{"teardown"}, "")
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	if rec.downCalls != 0 {
		t.Fatalf("무인자 down=%d회, want 0(dry-run)", rec.downCalls)
	}
	m := decode(t, out)
	if m["status"] != "dry_run" {
		t.Fatalf("status=%v, want dry_run; out=%s", m["status"], out)
	}
	if !strings.Contains(fmt.Sprint(m["next"]), "docker compose down") {
		t.Fatalf("down 계획 안내 없음: %v", m["next"])
	}
}

// TestTeardownDryRunPurgePlan — --purge alone is also a dry-run: only the -v plan, no execution.
func TestTeardownDryRunPurgePlan(t *testing.T) {
	withFake(t, &fakeStore{})
	rec := withFakeCompose(t)
	out, code := mustRun(t, []string{"teardown", "--purge"}, "")
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	if rec.downCalls != 0 {
		t.Fatalf("--purge 단독 down=%d회, want 0(dry-run)", rec.downCalls)
	}
	if m := decode(t, out); !strings.Contains(fmt.Sprint(m["next"]), "docker compose down -v") {
		t.Fatalf("-v 계획 안내 없음: %v", m["next"])
	}
}

// TestTeardownYes — only with --yes: stop guidance + exec delegation, exit 0 on success.
func TestTeardownYes(t *testing.T) {
	withFake(t, &fakeStore{})
	rec := withFakeCompose(t)
	out, code := mustRun(t, []string{"teardown", "--yes"}, "")
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	if rec.downCalls != 1 || rec.downPurge {
		t.Fatalf("down=%d purge=%v, want 1/false", rec.downCalls, rec.downPurge)
	}
	m := decode(t, out)
	if m["status"] != "ok" {
		t.Fatalf("status=%v, want ok; out=%s", m["status"], out)
	}
}

// TestTeardownPurge — with --yes --purge, the volume deletion (down -v) exec.
func TestTeardownPurge(t *testing.T) {
	withFake(t, &fakeStore{})
	rec := withFakeCompose(t)
	out, code := mustRun(t, []string{"teardown", "--yes", "--purge"}, "")
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	if rec.downCalls != 1 || !rec.downPurge {
		t.Fatalf("down=%d purge=%v, want 1/true", rec.downCalls, rec.downPurge)
	}
}

// TestTeardownFailGuidance — on stop failure, manual guidance is kept + exit 1.
func TestTeardownFailGuidance(t *testing.T) {
	withFake(t, &fakeStore{})
	rec := withFakeCompose(t)
	rec.downErr = context.DeadlineExceeded
	out, code := mustRun(t, []string{"teardown", "--yes", "--purge"}, "")
	if code != 1 {
		t.Fatalf("exit=%d, want 1; out=%s", code, out)
	}
	m := decode(t, out)
	w, _ := m["warnings"].([]any)
	if len(w) != 1 || !strings.Contains(fmt.Sprint(w[0]), "docker compose down -v") {
		t.Fatalf("수동 안내 없음: %v", m["warnings"])
	}
}

// storeDown — a seam where newStore always fails (DB fully down = the main setup scenario).
func storeDown(t *testing.T) {
	t.Helper()
	prev := newStore
	newStore = func(ctx context.Context, dsn string) (Store, error) {
		return nil, store.ErrServerDown
	}
	t.Cleanup(func() { newStore = prev })
}

// TestSetupDryStoreFree — even with the DB down (invalid DSN), setup --dry exits 0 (§13 consistency).
func TestSetupDryStoreFree(t *testing.T) {
	storeDown(t)
	rec := withFakeCompose(t)
	out, code := mustRun(t, []string{"setup", "--dry"}, "")
	if code != 0 {
		t.Fatalf("exit=%d, want 0(DB 다운에도 dry는 즉시 응답); out=%s", code, out)
	}
	if m := decode(t, out); m["status"] != "dry" {
		t.Fatalf("status=%v, want dry; out=%s", m["status"], out)
	}
	if rec.upCalls != 0 {
		t.Fatalf("dry에서 exec 발생: up=%d", rec.upCalls)
	}
}

// TestSetupNilStoreDegraded — running setup without a store (first run): no panic,
// degraded + guidance with exit 1 (the compose step still runs).
func TestSetupNilStoreDegraded(t *testing.T) {
	storeDown(t)
	rec := withFakeCompose(t)
	out, code := mustRun(t, []string{"setup"}, "")
	if code != 1 {
		t.Fatalf("exit=%d, want 1(degraded); out=%s", code, out)
	}
	if rec.upCalls != 1 {
		t.Fatalf("compose=%d회, want 1(DB 다운이어도 1단계는 수행)", rec.upCalls)
	}
	m := decode(t, out)
	if m["status"] != "degraded" {
		t.Fatalf("status=%v, want degraded; out=%s", m["status"], out)
	}
	if nexts, _ := m["next"].([]any); len(nexts) == 0 {
		t.Fatal("첫 실행 안내(next)가 유실됨")
	}
}

// TestTeardownStoreFree — even with the DB down, teardown does guidance and (--yes) exec.
func TestTeardownStoreFree(t *testing.T) {
	storeDown(t)
	rec := withFakeCompose(t)
	out, code := mustRun(t, []string{"teardown", "--yes"}, "")
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	if rec.downCalls != 1 {
		t.Fatalf("down=%d회, want 1", rec.downCalls)
	}
}

// withSetupPortProbe — swaps the 5434-occupancy detection seam + captures
// stderr. inUse=true means occupied.
func withSetupPortProbe(t *testing.T, inUse bool) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prevDial, prevErr := setupPortDialFn, setupStderr
	setupPortDialFn = func(addr string) bool { return inUse }
	setupStderr = &buf
	t.Cleanup(func() { setupPortDialFn, setupStderr = prevDial, prevErr })
	return &buf
}

// TestSetupPortInUseWarnsAndContinues — 5434 occupancy detected before compose start ->
// one warning line on stderr, progress continues (compose and migrate run on, exit 0).
func TestSetupPortInUseWarnsAndContinues(t *testing.T) {
	rec := withFakeCompose(t)
	errs := withSetupPortProbe(t, true)
	migrateCalled := false
	line, code := RunSetup(context.Background(),
		func() error { return nil },
		func() error { migrateCalled = true; return nil })
	if code != 0 {
		t.Fatalf("exit=%d, want 0(점유는 경고만); out=%s", code, line)
	}
	if rec.upCalls != 1 || !migrateCalled {
		t.Fatalf("진행 유지 위반: compose=%d회 migrate=%v", rec.upCalls, migrateCalled)
	}
	if !strings.Contains(errs.String(), "5434") {
		t.Fatalf("점유 경고에 5434 없음: %q", errs.String())
	}
	if lines := strings.Split(strings.TrimRight(errs.String(), "\n"), "\n"); len(lines) != 1 {
		t.Fatalf("경고는 1행이어야 한다: %q", errs.String())
	}
}

// TestSetupPortFreeNoWarning — when free, no stderr warning (progress identical).
func TestSetupPortFreeNoWarning(t *testing.T) {
	rec := withFakeCompose(t)
	errs := withSetupPortProbe(t, false)
	line, code := RunSetup(context.Background(),
		func() error { return nil },
		func() error { return nil })
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, line)
	}
	if rec.upCalls != 1 {
		t.Fatalf("compose=%d회, want 1", rec.upCalls)
	}
	if errs.Len() != 0 {
		t.Fatalf("미점유인데 경고 발생: %q", errs.String())
	}
}

// TestSyncRulesSkipsSymlinks — symbolic links in the source are
// not copied (blocking exposure of sensitive files at link targets).
func TestSyncRulesSkipsSymlinks(t *testing.T) {
	src := t.TempDir()
	dest := t.TempDir()
	secret := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(secret, []byte("topsecret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(src, "leak")); err != nil {
		t.Skip("심볼릭 링크 생성 불가 환경")
	}
	if err := os.WriteFile(filepath.Join(src, "normal.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	copied, err := syncRules(src, dest)
	if err != nil {
		t.Fatal(err)
	}
	if copied != 1 {
		t.Fatalf("복사 %d건, want 1(링크 제외)", copied)
	}
	if _, err := os.Stat(filepath.Join(dest, "leak")); !os.IsNotExist(err) {
		t.Errorf("심볼릭 링크가 복사됨: %v", err)
	}
}
