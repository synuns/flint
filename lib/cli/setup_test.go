package cli

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"flint/lib/store"
)

// composeRecorder — composeUpFn/composeDownFn seam 교체 기록(순수 단계테스트, exec 없음).
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

// TestSetupSteps — 4단계 고정 순서(①compose ②migrate ③seed ④플러그인).
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

// TestSetupAllGreen — fake ping/migrate 성공 → 요약 한 줄 JSON · exit 0.
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
		if strings.Contains(fmt.Sprint(n), "make seed") {
			found = true
		}
	}
	if !found {
		t.Fatalf("seed 안내(next) 없음: %v", m["next"])
	}
}

// TestSetupPingFailKeepsGuidance — ping 실패 시에도 다음 단계 안내 유지(protocol).
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

// TestSetupComposeFailNotFatal — docker 미설치 등은 경고로(안내 후 계속, exit 0).
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

// TestSetupDry — --dry는 단계만 출력, exec·store 연산 없음.
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

// TestSetupUnknownFlag — 알 수 없는 플래그는 한 줄 JSON 에러 + exit 1.
func TestSetupUnknownFlag(t *testing.T) {
	withFake(t, &fakeStore{})
	out, code := mustRun(t, []string{"setup", "--bogus"}, "")
	if code != 1 {
		t.Fatalf("exit=%d, want 1; out=%s", code, out)
	}
	decode(t, out)
}

// TestSetupRegistered — init() Register 확인.
func TestSetupRegistered(t *testing.T) {
	if _, ok := Lookup("setup"); !ok {
		t.Fatal("Lookup(\"setup\") 실패 — 미등록 커맨드")
	}
	if _, ok := Lookup("teardown"); !ok {
		t.Fatal("Lookup(\"teardown\") 실패 — 미등록 커맨드")
	}
}

// TestTeardownDown — 정지 안내 + exec 위임, 성공 시 exit 0.
func TestTeardownDown(t *testing.T) {
	withFake(t, &fakeStore{})
	rec := withFakeCompose(t)
	out, code := mustRun(t, []string{"teardown"}, "")
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

// TestTeardownPurge — --purge 시 volume 삭제(down -v) exec.
func TestTeardownPurge(t *testing.T) {
	withFake(t, &fakeStore{})
	rec := withFakeCompose(t)
	out, code := mustRun(t, []string{"teardown", "--purge"}, "")
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	if rec.downCalls != 1 || !rec.downPurge {
		t.Fatalf("down=%d purge=%v, want 1/true", rec.downCalls, rec.downPurge)
	}
}

// TestTeardownFailGuidance — 정지 실패 시 수동 안내 유지 + exit 1.
func TestTeardownFailGuidance(t *testing.T) {
	withFake(t, &fakeStore{})
	rec := withFakeCompose(t)
	rec.downErr = context.DeadlineExceeded
	out, code := mustRun(t, []string{"teardown", "--purge"}, "")
	if code != 1 {
		t.Fatalf("exit=%d, want 1; out=%s", code, out)
	}
	m := decode(t, out)
	w, _ := m["warnings"].([]any)
	if len(w) != 1 || !strings.Contains(fmt.Sprint(w[0]), "docker compose down -v") {
		t.Fatalf("수동 안내 없음: %v", m["warnings"])
	}
}

// storeDown — newStore가 항상 실패하는 seam(DB 전면 다운 = setup 주 시나리오).
func storeDown(t *testing.T) {
	t.Helper()
	prev := newStore
	newStore = func(ctx context.Context, dsn string) (Store, error) {
		return nil, store.ErrServerDown
	}
	t.Cleanup(func() { newStore = prev })
}

// TestSetupDryStoreFree — DB 다운(무효 DSN)에도 setup --dry는 exit 0(§13 정합).
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

// TestSetupNilStoreDegraded — store 없이(첫 실행) setup 실행: 패닉 없이
// degraded + 안내로 exit 1(compose 단계는 수행).
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

// TestTeardownStoreFree — DB 다운 상태에서도 teardown은 안내·exec 수행.
func TestTeardownStoreFree(t *testing.T) {
	storeDown(t)
	rec := withFakeCompose(t)
	out, code := mustRun(t, []string{"teardown"}, "")
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	if rec.downCalls != 1 {
		t.Fatalf("down=%d회, want 1", rec.downCalls)
	}
}
