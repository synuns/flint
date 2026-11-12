// done_test.go — done의 dispatch·realStore 위임 경로(등록 활성·seam).
// v0.39.0 T4: 직접 호출 단위 테스트는 lib/knowledge/done_test.go로 이동 —
// AutoBackupFn seam 교체도 knowledge 소유 var를 직접 스텁한다.
package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/knowledge"
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

// UpdateSessionSummary — v0.41.0 T3: 종료 요약 갱신 위임(쓰기 가드).
// fake는 no-op 성공(단위 경로 — 갱신 계약은 knowledge done_test가 단언).
func (d *doneFake) UpdateSessionSummary(ctx context.Context, id int, summary, tasks string) (int64, error) {
	return 1, nil
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

// UpdateSessionSummary — v0.41.0 T3: 쓰기 가드 전환분 — 실구현 위임.
func (d *delegatingDone) UpdateSessionSummary(ctx context.Context, id int, summary, tasks string) (int64, error) {
	return d.real.UpdateSessionSummary(ctx, id, summary, tasks)
}

// mustDoneRun — Run 전체 경로(등록·seam 포함).
func mustDoneRun(t *testing.T, args []string) (string, int) {
	t.Helper()
	stubAutoBackup(t)
	return mustRun(t, args, "")
}

// stubAutoBackup — knowledge.AutoBackupFn seam 교체(단위 테스트 무부작용
// 보장). v0.39.0 T4: seam var는 구현 소유 패키지(knowledge)에 있으므로
// 직접 스텁한다.
func stubAutoBackup(t *testing.T) {
	t.Helper()
	prev := knowledge.AutoBackupFn
	knowledge.AutoBackupFn = func(ctx context.Context, st Store) {}
	t.Cleanup(func() { knowledge.AutoBackupFn = prev })
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

// stubAutoBackup이 knowledge 소유 seam을 실제로 끄는지(위임 누락 방지).
func TestStubAutoBackupTargetsKnowledgeSeam(t *testing.T) {
	stubAutoBackup(t)
	var out strings.Builder
	// 직접 핸들러 호출 — knowledge.RunDone 경유.
	code := runDoneForSeamCheck(&out)
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out.String())
	}
}

// runDoneForSeamCheck — 등록된 done 핸들러 경유 실행(최소 세션).
func runDoneForSeamCheck(out *strings.Builder) int {
	h, ok := Lookup("done")
	if !ok {
		return -1
	}
	return h(context.Background(), &fakeStore{}, []string{"--session", "claude"}, strings.NewReader(""), out)
}
