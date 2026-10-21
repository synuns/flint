package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
)

// doneFake — fakeStore에 StaleSessionRuns 스텁을 얹은 fake.
// stub(stubbed) 미지정 시 영(零) 동작, SUPERLITE_REAL=1이면 실구현 위임.
type doneFake struct {
	*fakeStore

	stubbed bool // runs·runsErr 중 하나라도 지정됐는가
	runs    int
	runsErr error

	// 기록
	gotAgent string
}

func (d *doneFake) StaleSessionRuns(ctx context.Context, agent string) (int, error) {
	d.gotAgent = agent
	if d.runsErr != nil {
		return 0, d.runsErr
	}
	return d.runs, nil
}

// withDoneFake — Run 경로 seam. 기본 fake, SUPERLITE_REAL=1이면 stub
// 미지정 연산을 실구현에 위임(실DB 미기동 시 스킵).
func withDoneFake(t *testing.T, d *doneFake) {
	t.Helper()
	prev := newStore
	if os.Getenv("SUPERLITE_REAL") == "1" {
		real, err := newRealStore(context.Background(), dsnFromEnv())
		if err != nil {
			t.Skipf("real store unavailable: %v", err)
		}
		t.Cleanup(real.Close)
		rs := real.(*realStore)
		newStore = func(ctx context.Context, dsn string) (Store, error) {
			return &delegatingDone{done: d, real: rs}, nil
		}
	} else {
		newStore = func(ctx context.Context, dsn string) (Store, error) {
			return d, nil
		}
	}
	t.Cleanup(func() { newStore = prev })
}

// delegatingDone — stub 지정 연산은 fake, 미지정은 실구현 위임.
type delegatingDone struct {
	done *doneFake
	real *realStore
}

func (d *delegatingDone) Migrate(ctx context.Context) error { return d.real.Migrate(ctx) }
func (d *delegatingDone) InsertBatch(ctx context.Context, recs []map[string]any) ([]int, error) {
	return d.real.InsertBatch(ctx, recs)
}
func (d *delegatingDone) ExistsHash(ctx context.Context, hash string) (bool, error) {
	return d.real.ExistsHash(ctx, hash)
}
func (d *delegatingDone) Commit(ctx context.Context, msg string) (string, error) {
	return d.real.Commit(ctx, msg)
}
func (d *delegatingDone) Ping(ctx context.Context) error { return d.real.Ping(ctx) }
func (d *delegatingDone) Close()                         { d.real.Close() }
func (d *delegatingDone) Query(ctx context.Context, sql string, args ...any) ([][]any, error) {
	if d.done.queryFn != nil || d.done.queryErr != nil {
		return d.done.Query(ctx, sql, args...)
	}
	return d.real.Query(ctx, sql, args...)
}
func (d *delegatingDone) StaleSessionRuns(ctx context.Context, agent string) (int, error) {
	if d.done.stubbed {
		return d.done.StaleSessionRuns(ctx, agent)
	}
	return d.real.StaleSessionRuns(ctx, agent)
}

// mustDone — runDone 직접 실행(root.go 등록과 무관한 단위 경로).
// 자동 백업 트리거는 no-op 스텁(실동작은 e2e ④가 실DB로 검증).
func mustDone(t *testing.T, st Store, args []string) (string, int) {
	t.Helper()
	stubAutoBackup(t)
	var out bytes.Buffer
	code := runDone(context.Background(), st, args, strings.NewReader(""), &out)
	return out.String(), code
}

// mustDoneRun — Run 전체 경로(등록·seam 포함).
func mustDoneRun(t *testing.T, args []string) (string, int) {
	t.Helper()
	stubAutoBackup(t)
	return mustRun(t, args, "")
}

// stubAutoBackup — autoBackupFn seam 교체(단위 테스트 무부작용 보장).
func stubAutoBackup(t *testing.T) {
	t.Helper()
	prev := autoBackupFn
	autoBackupFn = func(ctx context.Context) {}
	t.Cleanup(func() { autoBackupFn = prev })
}

// done 판정 뒤 자동 백업 훅 호출(stale·ok 양 경로).
func TestDoneTriggersAutoBackupHook(t *testing.T) {
	for _, runs := range []int{0, 3} { // ok·stale 양쪽에서 1회씩
		f := &fakeStore{}
		d := &doneFake{fakeStore: f, stubbed: true, runs: runs}
		called := 0
		prev := autoBackupFn
		autoBackupFn = func(ctx context.Context) { called++ }
		t.Cleanup(func() { autoBackupFn = prev })
		var out bytes.Buffer
		code := runDone(context.Background(), d,
			[]string{"--session", "claude"}, strings.NewReader(""), &out)
		if code != 0 {
			t.Fatalf("runs=%d exit=%d, want 0; out=%s", runs, code, out.String())
		}
		if called != 1 {
			t.Fatalf("runs=%d 자동 백업 훅 %d회, want 1", runs, called)
		}
	}
}

// Review Focus(protocol): runs 2 vs 3 경계 — 3에서 warning 포함·exit 0.
func TestDoneStaleBoundary(t *testing.T) {
	cases := []struct {
		runs   int
		status string
	}{
		{2, "ok"}, // 임계값 직전
		{3, "stale"}, // 기본 StaleSessions=3 도달
	}
	for _, c := range cases {
		f := &fakeStore{}
		d := &doneFake{fakeStore: f, stubbed: true, runs: c.runs}
		out, code := mustDone(t, d, []string{"--session", "claude"})
		if code != 0 {
			t.Fatalf("runs=%d exit=%d, want 0(종료 불가 원칙); out=%s", c.runs, code, out)
		}
		m := decode(t, out)
		if m["status"] != c.status {
			t.Fatalf("runs=%d status=%v, want %s; out=%s", c.runs, m["status"], c.status, out)
		}
		if c.status == "stale" {
			if m["runs"] != float64(c.runs) {
				t.Fatalf("runs=%v, want %d", m["runs"], c.runs)
			}
			w, _ := m["warning"].(string)
			if !strings.Contains(w, "연속 3세션 미기록") {
				t.Fatalf("warning=%q, want 연속 3세션 미기록", w)
			}
		}
		if d.gotAgent != "claude" {
			t.Fatalf("집계 agent=%q, want claude", d.gotAgent)
		}
	}
}

// meta 임계값 우선 — stale_sessions=5면 runs=3도 ok, =2면 runs=2도 stale.
func TestDoneUsesMetaThreshold(t *testing.T) {
	cases := []struct {
		meta   string
		runs   int
		status string
	}{
		{"5", 3, "ok"},
		{"2", 2, "stale"},
	}
	for _, c := range cases {
		f := &fakeStore{}
		f.queryFn = func(sql string, args []any) ([][]any, error) {
			if strings.Contains(sql, "FROM meta") {
				return [][]any{{"stale_sessions", c.meta}}, nil
			}
			return nil, nil
		}
		d := &doneFake{fakeStore: f, stubbed: true, runs: c.runs}
		out, code := mustDone(t, d, []string{"--session", "claude"})
		if code != 0 {
			t.Fatalf("meta=%s exit=%d, want 0; out=%s", c.meta, code, out)
		}
		if m := decode(t, out); m["status"] != c.status {
			t.Fatalf("meta=%s runs=%d status=%v, want %s", c.meta, c.runs, m["status"], c.status)
		}
	}
}

// Review Focus(protocol): 조회 실패 경로도 exit 0(비간섭, 한 줄 JSON).
func TestDoneErrorStillExit0(t *testing.T) {
	f := &fakeStore{queryErr: errors.New("meta blew up")}
	d := &doneFake{fakeStore: f, stubbed: true, runsErr: errors.New("stale blew up")}
	out, code := mustDone(t, d, []string{"--session", "claude"})
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	decode(t, out) // 한 줄 JSON이면 충분
}

// Store가 StaleSessionRuns를 미구현(레거시 fake)이면 경고 없는 ok가 기본값.
func TestDoneStoreWithoutStaleImpl(t *testing.T) {
	f := &fakeStore{}
	out, code := mustDone(t, f, []string{"--session", "claude"})
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	if m := decode(t, out); m["status"] != "ok" {
		t.Fatalf("status=%v, want ok", m["status"])
	}
}

// 숫자 세션 id는 sessions.agent 조회로 대상을 찾는다.
func TestDoneAgentLookupBySessionID(t *testing.T) {
	f := &fakeStore{}
	f.queryFn = func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM sessions") {
			return [][]any{{"claude"}}, nil
		}
		return nil, nil
	}
	d := &doneFake{fakeStore: f, stubbed: true, runs: 3}
	out, code := mustDone(t, d, []string{"--session", "7"})
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	if d.gotAgent != "claude" {
		t.Fatalf("agent=%q, want claude(sessions 조회 결과)", d.gotAgent)
	}
	if m := decode(t, out); m["status"] != "stale" {
		t.Fatalf("status=%v, want stale", m["status"])
	}
}

// 등록·Run 전체 경로 — Lookup 성공 + exit 0.
func TestDoneRegisteredAndRun(t *testing.T) {
	if _, ok := Lookup("done"); !ok {
		t.Fatal("done 미등록")
	}
	f := &fakeStore{}
	withFake(t, f)
	out, code := mustDoneRun(t, []string{"done", "--session", "claude"})
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	if m := decode(t, out); m["status"] != "ok" {
		t.Fatalf("status=%v, want ok", m["status"])
	}
}

// seam 검증 — stub 미지정 시 실구현 위임(SUPERLITE_REAL=1 실DB, 아니면 fake).
func TestDoneDelegatingRun(t *testing.T) {
	f := &fakeStore{}
	d := &doneFake{fakeStore: f}
	withDoneFake(t, d)
	out, code := mustDoneRun(t, []string{"done", "--session", "done-seam-probe"})
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	decode(t, out)
}
