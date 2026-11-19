// done_test.go — the done verdict contract (the direct-call path — the
// dispatch·realStore delegation paths belong to the cli-residual
// done_test).
package knowledge

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/synuns/flint/lib/wire"
)

// doneFake — a fakeStore with the StaleSessionRuns stub layered on. A
// no-op when the stub (stubbed) is unset (the same contract as the
// cli-residual doneFake).
type doneFake struct {
	*fakeStore

	stubbed bool // whether runs·runsErr is set
	runs    int
	runsErr error

	// recording
	gotAgent string
	// UpdateSessionSummary delegation capture
	updatedSessions []int
}

func (d *doneFake) StaleSessionRuns(ctx context.Context, agent string) (int, error) {
	d.gotAgent = agent
	if d.runsErr != nil {
		return 0, d.runsErr
	}
	return d.runs, nil
}

// UpdateSessionSummary — the exit-summary renewal delegates to
// db.Repo.UpdateSessionSummary (the write guard) — the fake records the
// renewed id.
func (d *doneFake) UpdateSessionSummary(ctx context.Context, id int, summary, tasks string) (int64, error) {
	d.updatedSessions = append(d.updatedSessions, id)
	return 1, nil
}

// mustDone — runs RunDone directly (the unit path, independent of the
// root.go registration). The automatic-backup trigger is a no-op stub
// (the real action is verified against a real DB by e2e ④).
func mustDone(t *testing.T, st wire.Store, args []string) (string, int) {
	t.Helper()
	stubAutoBackup(t)
	var out bytes.Buffer
	code := RunDone(context.Background(), st, args, strings.NewReader(""), &out)
	return out.String(), code
}

// stubAutoBackup — swaps the AutoBackupFn seam (guaranteeing
// side-effect-free unit tests).
func stubAutoBackup(t *testing.T) {
	t.Helper()
	prev := AutoBackupFn
	// st parameter extension — the stub ignores it.
	AutoBackupFn = func(ctx context.Context, st wire.Store) {}
	t.Cleanup(func() { AutoBackupFn = prev })
}

// The automatic-backup hook fires after the done verdict (both the
// stale·ok paths).
func TestDoneTriggersAutoBackupHook(t *testing.T) {
	for _, runs := range []int{0, 3} { // once on each of the ok·stale sides
		f := &fakeStore{}
		d := &doneFake{fakeStore: f, stubbed: true, runs: runs}
		called := 0
		prev := AutoBackupFn
		AutoBackupFn = func(ctx context.Context, st wire.Store) { called++ }
		t.Cleanup(func() { AutoBackupFn = prev })
		var out bytes.Buffer
		code := RunDone(context.Background(), d,
			[]string{"--session", "claude"}, strings.NewReader(""), &out)
		if code != 0 {
			t.Fatalf("runs=%d exit=%d, want 0; out=%s", runs, code, out.String())
		}
		if called != 1 {
			t.Fatalf("runs=%d 자동 백업 훅 %d회, want 1", runs, called)
		}
	}
}

// The runs 2 vs 3 boundary — at 3 the warning is included·exit 0.
func TestDoneStaleBoundary(t *testing.T) {
	cases := []struct {
		runs   int
		status string
	}{
		{2, "ok"},    // just under the threshold
		{3, "stale"}, // reaches the default StaleSessions=3
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
			if !strings.Contains(w, "3 consecutive sessions unrecorded") {
				t.Fatalf("warning=%q, want 3 consecutive sessions unrecorded", w)
			}
		}
		if d.gotAgent != "claude" {
			t.Fatalf("집계 agent=%q, want claude", d.gotAgent)
		}
	}
}

// The meta threshold wins — with stale_sessions=5, runs=3 is still ok;
// with =2, runs=2 is already stale.
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

// Even the lookup-failure path exits 0 (non-interference, one-line
// JSON).
func TestDoneErrorStillExit0(t *testing.T) {
	f := &fakeStore{queryErr: errors.New("meta blew up")}
	d := &doneFake{fakeStore: f, stubbed: true, runsErr: errors.New("stale blew up")}
	out, code := mustDone(t, d, []string{"--session", "claude"})
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	decode(t, out) // a one-line JSON suffices
}

// A Store not implementing StaleSessionRuns (a legacy fake) defaults to
// a warning-free ok.
func TestDoneStoreWithoutStaleImpl(t *testing.T) {
	f := &fakeStore{}
	out, code := mustDone(t, f, []string{"--session", "claude"})
	if code != 0 {
		t.Fatalf("exit=%d, want 0", code)
	}
	if m := decode(t, out); m["status"] != "ok" {
		t.Fatalf("status=%v, want ok", m["status"])
	}
}

// A numeric session id resolves the target via the sessions.agent
// lookup.
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

// The SessionRecord contract — the exit-summary marker·empty tasks.
func TestDoneSessionRecord(t *testing.T) {
	d := &DoneResult{Agent: "claude", Unrecorded: 2}
	rec := d.SessionRecord()
	if rec["kind"] != "session" || rec["agent"] != "claude" {
		t.Fatalf("kind/agent=%v/%v, want session/claude", rec["kind"], rec["agent"])
	}
	if rec["summary"] != "session end (unrecorded 2)" {
		t.Fatalf("summary=%v, want session end (unrecorded 2)", rec["summary"])
	}
	if rec["tasks"] != "[]" {
		t.Fatalf("tasks=%v, want []", rec["tasks"])
	}
}

// Running done records the session summary record (verified via fake
// store calls)·output unchanged.
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
	// done accrues the session record + session knowledgization (pending)
	// — verified per kind (done_v0270_test owns the knowledge contract
	// alone).
	sessions, knowledge := 0, 0
	for _, rec := range f.inserted {
		if rec["kind"] == "session" {
			sessions++
		} else if rec["kind"] == "knowledge" {
			knowledge++
		}
	}
	if sessions != 1 {
		t.Fatalf("세션 레코드 삽입 %d건, want 1; inserted=%v", sessions, f.inserted)
	}
	if knowledge > 1 {
		t.Fatalf("지식 적립 %d건, want ≤1(빈도 상한)", knowledge)
	}
	if got := f.inserted[0]["summary"]; got != "session end (unrecorded 1)" {
		t.Fatalf("삽입 summary=%v, want session end (unrecorded 1)", got)
	}
	if got := f.inserted[0]["agent"]; got != "claude" {
		t.Fatalf("삽입 agent=%v, want claude", got)
	}
}

// TestDoneStaleWarningReachable — a real-wiring reachability
// regression: the hook passes the Claude session_id (a UUID) as
// --session. If the UUID became the agent verbatim, each session would
// split into its own agent and StaleSessionRuns could never exceed 1,
// so the stale warning would never fire. Distinct UUID sessions ending
// must stabilize the aggregated agent on the harness default (claude)
// so the warning actually fires on the 4th done when the threshold
// (default 3) is reached.
func TestDoneStaleWarningReachable(t *testing.T) {
	sessions := []string{
		"3f2a1c9e-6b7d-4c8e-9f0a-1b2c3d4e5f67",
		"8e1d0b7a-4c2f-4a9b-b3c5-7d6e5f4a3b21",
		"c5b9a2d4-7e3f-4d1a-8f2b-9a1b2c3d4e5f",
	}
	f := &fakeStore{}
	d := &doneFake{fakeStore: f, stubbed: true}
	// 3 distinct sessions (the count-then-accrue model — session rows
	// pile up after done counts them, so 0→1→2) — whichever UUID ends,
	// the aggregated agent stabilizes on one and it stays just under the
	// threshold, so ok.
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
	// The 4th done — the accumulated count reaches 3, the warning
	// actually fires.
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
	// The sessions row's agent is also the stable identifier — the
	// aggregation key of the accumulated count. The knowledgization
	// pending (kind knowledge) also uses an agent-based topic, but
	// carries no agent key — only rows whose kind is session are
	// verified.
	for i, rec := range f.inserted {
		if rec["kind"] == "session" && rec["agent"] != "claude" {
			t.Fatalf("삽입 %d건째 agent=%v, want claude", i, rec["agent"])
		}
	}
}

// Only the UUID shape (the real hook-wiring shape) aggregates into
// claude.
func TestAgentForSessionUUIDMapsClaude(t *testing.T) {
	f := &fakeStore{}
	d := &doneFake{fakeStore: f, stubbed: true}
	for _, sid := range []string{
		"3f2a1c9e-6b7d-4c8e-9f0a-1b2c3d4e5f67",
		"D1C2B3A4-9F8E-4D7C-B6A5-4E3F2D1C0B9A", // uppercase hex is also a UUID
	} {
		if got := agentForSession(context.Background(), d, sid); got != "claude" {
			t.Fatalf("UUID %s agent=%q, want claude", sid, got)
		}
	}
}

// A non-UUID uninterpreted string (e2e·another harness) is used as the
// agent verbatim. Absorbing it into claude would overwrite a real claude
// session's summary and misfire the stale warning.
func TestAgentForSessionNonUUIDStaysOwn(t *testing.T) {
	f := &fakeStore{}
	d := &doneFake{fakeStore: f, stubbed: true}
	for _, sid := range []string{
		"e2e-backup-probe",
		"codex-xyz",
		"done-seam-probe",
		"42", // numeric id unfound — the original string kept
	} {
		if got := agentForSession(context.Background(), d, sid); got != sid {
			t.Fatalf("비-UUID %q agent=%q, want 원본 문자열(고립 행)", sid, got)
		}
	}
}

// On a second done, renewal (no duplicate rows) — if the latest session
// carries the exit marker, UPDATE only.
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
		// The renewal delegates to UpdateSessionSummary — executed once
		// per round with the latest session id (7).
		if len(d.updatedSessions) != 1 || d.updatedSessions[0] != 7 {
			t.Fatalf("%d회차 UPDATE sessions 미실행(위임 캡처=%v)", i+1, d.updatedSessions)
		}
	}
	// Session-row renewal (not INSERT) — only the knowledgization pending
	// remains in inserted (normal). Asserts the absence of a session-kind
	// INSERT.
	for _, rec := range f.inserted {
		if rec["kind"] == "session" {
			t.Fatalf("갱신 경로에서 세션 INSERT 발생(중복 행 금지 위반): %v", rec)
		}
	}
}

// TestDoneAutoBackupNoExtraPool — maybeAutoBackup never opens its own
// pool (db.Connect) and reaches meta through the injected st. A
// self-pool-first implementation on an unreachable DSN would quietly
// return before the st lookup and leave the fake record empty.
func TestDoneAutoBackupNoExtraPool(t *testing.T) {
	t.Setenv("FLINT_BACKUP_DIR", t.TempDir())
	t.Setenv("FLINT_DSN", "postgresql://flint@127.0.0.1:1/flint?sslmode=disable&connect_timeout=1")
	f := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM meta") {
			// last_backup=just now → no dump fired (there must be no
			// connection attempt at all).
			return [][]any{{time.Now().UTC().Format(time.RFC3339)}}, nil
		}
		return nil, nil
	}}
	maybeAutoBackup(context.Background(), f)
	metaSeen := false
	for _, q := range f.queries {
		if strings.Contains(q, "FROM meta") {
			metaSeen = true
		}
	}
	if !metaSeen {
		t.Fatalf("meta 조회가 주입 st를 경유하지 않음(queries=%d) — 자체 풀 경로 잔존 의심", len(f.queries))
	}
}
