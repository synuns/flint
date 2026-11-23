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

type doneFake struct {
	*fakeStore

	stubbed bool
	runs    int
	runsErr error

	gotAgent string

	updatedSessions []int
}

func (d *doneFake) StaleSessionRuns(ctx context.Context, agent string) (int, error) {
	d.gotAgent = agent
	if d.runsErr != nil {
		return 0, d.runsErr
	}
	return d.runs, nil
}

func (d *doneFake) UpdateSessionSummary(ctx context.Context, id int, summary, tasks string) (int64, error) {
	d.updatedSessions = append(d.updatedSessions, id)
	return 1, nil
}

func mustDone(t *testing.T, st wire.Store, args []string) (string, int) {
	t.Helper()
	stubAutoBackup(t)
	var out bytes.Buffer
	code := RunDone(context.Background(), st, args, strings.NewReader(""), &out)
	return out.String(), code
}

func stubAutoBackup(t *testing.T) {
	t.Helper()
	prev := AutoBackupFn

	AutoBackupFn = func(ctx context.Context, st wire.Store) {}
	t.Cleanup(func() { AutoBackupFn = prev })
}

func TestDoneTriggersAutoBackupHook(t *testing.T) {
	for _, runs := range []int{0, 3} {
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

func TestDoneStaleBoundary(t *testing.T) {
	cases := []struct {
		runs   int
		status string
	}{
		{2, "ok"},
		{3, "stale"},
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

func TestDoneErrorStillExit0(t *testing.T) {
	f := &fakeStore{queryErr: errors.New("meta blew up")}
	d := &doneFake{fakeStore: f, stubbed: true, runsErr: errors.New("stale blew up")}
	out, code := mustDone(t, d, []string{"--session", "claude"})
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	decode(t, out)
}

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

func TestDoneStaleWarningReachable(t *testing.T) {
	sessions := []string{
		"3f2a1c9e-6b7d-4c8e-9f0a-1b2c3d4e5f67",
		"8e1d0b7a-4c2f-4a9b-b3c5-7d6e5f4a3b21",
		"c5b9a2d4-7e3f-4d1a-8f2b-9a1b2c3d4e5f",
	}
	f := &fakeStore{}
	d := &doneFake{fakeStore: f, stubbed: true}

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

	for i, rec := range f.inserted {
		if rec["kind"] == "session" && rec["agent"] != "claude" {
			t.Fatalf("삽입 %d건째 agent=%v, want claude", i, rec["agent"])
		}
	}
}

func TestAgentForSessionUUIDMapsClaude(t *testing.T) {
	f := &fakeStore{}
	d := &doneFake{fakeStore: f, stubbed: true}
	for _, sid := range []string{
		"3f2a1c9e-6b7d-4c8e-9f0a-1b2c3d4e5f67",
		"D1C2B3A4-9F8E-4D7C-B6A5-4E3F2D1C0B9A",
	} {
		if got := agentForSession(context.Background(), d, sid); got != "claude" {
			t.Fatalf("UUID %s agent=%q, want claude", sid, got)
		}
	}
}

func TestAgentForSessionNonUUIDStaysOwn(t *testing.T) {
	f := &fakeStore{}
	d := &doneFake{fakeStore: f, stubbed: true}
	for _, sid := range []string{
		"e2e-backup-probe",
		"codex-xyz",
		"done-seam-probe",
		"42",
	} {
		if got := agentForSession(context.Background(), d, sid); got != sid {
			t.Fatalf("비-UUID %q agent=%q, want 원본 문자열(고립 행)", sid, got)
		}
	}
}

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

		if len(d.updatedSessions) != 1 || d.updatedSessions[0] != 7 {
			t.Fatalf("%d회차 UPDATE sessions 미실행(위임 캡처=%v)", i+1, d.updatedSessions)
		}
	}

	for _, rec := range f.inserted {
		if rec["kind"] == "session" {
			t.Fatalf("갱신 경로에서 세션 INSERT 발생(중복 행 금지 위반): %v", rec)
		}
	}
}

func TestDoneAutoBackupNoExtraPool(t *testing.T) {
	t.Setenv("FLINT_BACKUP_DIR", t.TempDir())
	t.Setenv("FLINT_DSN", "postgresql://flint@127.0.0.1:1/flint?sslmode=disable&connect_timeout=1")
	f := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM meta") {

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
