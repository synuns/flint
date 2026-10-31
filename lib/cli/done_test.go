package cli

import (
	"bytes"
	"context"
	"errors"
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

// withDoneFake — Run 경로 seam. 기본 fake, FLINT_TEST_DSN 설정 +
// SUPERLITE_REAL=1이면 stub 미지정 연산을 실구현에 위임(실DB 미기동 시
// 스킵). FLINT_TEST_DSN 미설정 시 실구현 폴백 금지(N1 — seamRealDSN).
func withDoneFake(t *testing.T, d *doneFake) {
	t.Helper()
	prev := newStore
	if dsn, real := seamRealDSN(); real {
		realSt, err := newRealStore(context.Background(), dsn)
		if err != nil {
			t.Skipf("real store unavailable: %v", err)
		}
		t.Cleanup(realSt.Close)
		rs := realSt.(*realStore)
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
func (d *delegatingDone) ExistsProject(ctx context.Context, name string) (bool, error) {
	return d.real.ExistsProject(ctx, name)
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
		{2, "ok"},    // 임계값 직전
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

// SessionRecord 계약 — 종료 요약 마커·빈 tasks.
func TestDoneSessionRecord(t *testing.T) {
	d := &DoneResult{Agent: "claude", Unrecorded: 2}
	rec := d.SessionRecord()
	if rec["kind"] != "session" || rec["agent"] != "claude" {
		t.Fatalf("kind/agent=%v/%v, want session/claude", rec["kind"], rec["agent"])
	}
	if rec["summary"] != "세션 종료(미기록 2)" {
		t.Fatalf("summary=%v, want 세션 종료(미기록 2)", rec["summary"])
	}
	if rec["tasks"] != "[]" {
		t.Fatalf("tasks=%v, want []", rec["tasks"])
	}
}

// done 실행 시 세션 요약 레코드 기록(fake store 호출 검증)·출력 불변.
func TestDoneWritesSession(t *testing.T) {
	f := &fakeStore{}
	d := &doneFake{fakeStore: f, stubbed: true, runs: 1}
	out, code := mustDone(t, d, []string{"--session", "claude"})
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	if m := decode(t, out); m["status"] != "ok" {
		t.Fatalf("status=%v, want ok(출력 불변)", m["status"])
	}
	if len(f.inserted) != 1 {
		t.Fatalf("세션 레코드 삽입 %d건, want 1; inserted=%v", len(f.inserted), f.inserted)
	}
	if got := f.inserted[0]["summary"]; got != "세션 종료(미기록 1)" {
		t.Fatalf("삽입 summary=%v, want 세션 종료(미기록 1)", got)
	}
	if got := f.inserted[0]["agent"]; got != "claude" {
		t.Fatalf("삽입 agent=%v, want claude", got)
	}
}

// TestDoneStaleWarningReachable — 실배선 도달성 회귀(코드리뷰 #3): 훅은
// Claude session_id(UUID)를 --session으로 넘긴다. UUID가 그대로 agent가
// 되면 세션마다 agent가 갈라져 StaleSessionRuns가 1을 못 넘어 stale 경고가
// 영구히 발화되지 않는다(T17/T44 무효). 서로 다른 UUID 세션이 끝나도
// 집계 agent는 하니스 기본(claude)으로 안정되어야 임계값(기본 3) 도달
// 시 4번째 done에서 경고가 실제 발화된다.
func TestDoneStaleWarningReachable(t *testing.T) {
	sessions := []string{
		"3f2a1c9e-6b7d-4c8e-9f0a-1b2c3d4e5f67",
		"8e1d0b7a-4c2f-4a9b-b3c5-7d6e5f4a3b21",
		"c5b9a2d4-7e3f-4d1a-8f2b-9a1b2c3d4e5f",
	}
	f := &fakeStore{}
	d := &doneFake{fakeStore: f, stubbed: true}
	// 서로 다른 세션 3회(카운트 후 적립 모델 — 세션 행은 done이 세고 나서
	// 쌓이므로 0→1→2) — 어느 UUID로 끝나도 집계 agent는 하나로 안정되고
	// 임계값 직전이라 ok.
	for i, sid := range sessions {
		d.runs = i
		d.gotAgent = ""
		out, code := mustDone(t, d, []string{"--session", sid})
		if code != 0 {
			t.Fatalf("%d세션 exit=%d, want 0; out=%s", i+1, code, out)
		}
		if d.gotAgent != "claude" {
			t.Fatalf("%d세션 집계 agent=%q, want claude(세션 UUID가 새면 누적 불가)", i+1, d.gotAgent)
		}
		if m := decode(t, out); m["status"] != "ok" {
			t.Fatalf("%d세션 status=%v, want ok(임계값 직전); out=%s", i+1, m["status"], out)
		}
	}
	// 4번째 done — 누적 3 도달, 경고 실제 발화.
	uuid4 := "d1c2b3a4-9f8e-4d7c-b6a5-4e3f2d1c0b9a"
	d.runs = 3
	out, code := mustDone(t, d, []string{"--session", uuid4})
	if code != 0 {
		t.Fatalf("4번째 exit=%d, want 0(경고여도 종료); out=%s", code, out)
	}
	if d.gotAgent != "claude" {
		t.Fatalf("4번째 집계 agent=%q, want claude", d.gotAgent)
	}
	m := decode(t, out)
	if m["status"] != "stale" || m["runs"] != float64(3) {
		t.Fatalf("4번째 출력=%v, want stale/3(경고 실제 발화)", m)
	}
	// sessions 행 agent도 안정 식별자 — 누적 카운트의 집계 키.
	for i, rec := range f.inserted {
		if rec["agent"] != "claude" {
			t.Fatalf("삽입 %d건째 agent=%v, want claude", i+1, rec["agent"])
		}
	}
}

// 2차 리뷰 #1 — UUID 형식(훅 실배선 형식)만 claude로 집계된다.
func TestAgentForSessionUUIDMapsClaude(t *testing.T) {
	f := &fakeStore{}
	d := &doneFake{fakeStore: f, stubbed: true}
	for _, sid := range []string{
		"3f2a1c9e-6b7d-4c8e-9f0a-1b2c3d4e5f67",
		"D1C2B3A4-9F8E-4D7C-B6A5-4E3F2D1C0B9A", // 대문자 hex도 UUID
	} {
		if got := agentForSession(context.Background(), d, sid); got != "claude" {
			t.Fatalf("UUID %s agent=%q, want claude", sid, got)
		}
	}
}

// 2차 리뷰 #1 — UUID가 아닌 미해석 문자열(e2e·타 하니스)은 그 값 자체를
// agent로 쓴다. claude로 흡수되면 실제 claude 세션 summary를 덮어쓰고
// stale 경고가 오발화된다.
func TestAgentForSessionNonUUIDStaysOwn(t *testing.T) {
	f := &fakeStore{}
	d := &doneFake{fakeStore: f, stubbed: true}
	for _, sid := range []string{
		"e2e-backup-probe",
		"codex-xyz",
		"done-seam-probe",
		"42", // 숫자 id 미발견 — 원본 문자열 유지
	} {
		if got := agentForSession(context.Background(), d, sid); got != sid {
			t.Fatalf("비-UUID %q agent=%q, want 원본 문자열(고립 행)", sid, got)
		}
	}
}

// 2회 done 시 갱신(중복 행 금지) — 최근 세션이 종료 마커면 UPDATE만.
func TestDoneUpdatesExistingSession(t *testing.T) {
	f := &fakeStore{}
	f.queryFn = func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM sessions") && strings.Contains(sql, "LIMIT 1") {
			return [][]any{{int64(7), "세션 종료(미기록 0)"}}, nil
		}
		return nil, nil
	}
	for i := 0; i < 2; i++ {
		d := &doneFake{fakeStore: f, stubbed: true, runs: i + 1}
		out, code := mustDone(t, d, []string{"--session", "claude"})
		if code != 0 {
			t.Fatalf("%d회차 exit=%d, want 0; out=%s", i+1, code, out)
		}
		decode(t, out)
	}
	if len(f.inserted) != 0 {
		t.Fatalf("갱신 경로에서 삽입 %d건, want 0(중복 행 금지)", len(f.inserted))
	}
	updated := false
	for _, q := range f.queries {
		if strings.HasPrefix(q, "UPDATE sessions") {
			updated = true
		}
	}
	if !updated {
		t.Fatalf("UPDATE sessions 미실행: %v", f.queries)
	}
}
