// dual_device_test.go — the v1.9.0 T6 dual-device regression
// (acceptance criteria — spec requirement 6). Reproduces the R38 fatal
// scenario (both sides' working sets carrying read payloads → pull
// merge panic / same-knowledge-row merge impossible) after the recall
// append-only (schema v12) switch, measuring no-panic·event union·new-
// row coexistence·derived-ranking equality. Isolated DBs A·B (scratch
// creation·trailing DROP), a file:// remote (the R34 isolation
// convention·inheriting the TestSyncPushPullRoundTripRealSlow
// pattern), sharedDBLock mutual exclusion. Synthetic demo data only
// (personal data forbidden).
package db

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// t6Commit — a full-staging commit on the scratch DB (a test-owned
// harness — independent of the flint runtime's -A-abolition contract,
// inheriting the sync_test convention). The initial commit must be -A
// to carry the full schema (tables·views·sequences) the migration
// created to the remote.
func t6Commit(t *testing.T, q Querier, msg string) {
	t.Helper()
	if _, err := q.Query(context.Background(), `SELECT dolt_add('-A')`); err != nil {
		t.Fatalf("dolt_add: %v", err)
	}
	if _, err := q.Query(context.Background(), `SELECT dolt_commit('-m', $1)`, msg); err != nil {
		t.Fatalf("dolt_commit(%q): %v", msg, err)
	}
}

// t6NoPanic — asserts that a remote-operation error is not an engine
// panic (the v1.7.2 S1 mitigation target — the "caught panic" detection
// string·ErrRemotePanic·XX000). The core verdict of the R38 fatal
// regression: if the mitigation guidance (RemotePanicGuide) appears, it
// has already failed.
func t6NoPanic(t *testing.T, op string, err error) {
	t.Helper()
	if err == nil {
		return
	}
	if errors.Is(err, ErrRemotePanic) {
		t.Fatalf("%s: 엔진 패닉(ErrRemotePanic) — R38 치명 재현: %v", op, err)
	}
	low := strings.ToLower(err.Error())
	if strings.Contains(low, "caught panic") || strings.Contains(low, "xx000") ||
		strings.Contains(err.Error(), RemotePanicGuide) {
		t.Fatalf("%s: 패닉 감지 문자/완화 안내 발생 — R38 치명 재현: %v", op, err)
	}
	t.Fatalf("%s: 원격 연산 실패(패닉 외 원인도 수용 기준 미달): %v", op, err)
}

// t6RecallCounts — knowledge id → recall event count (the same
// definition as the derived ranking's source aggregate). The material
// for the union·ranking-equality verdicts.
func t6RecallCounts(t *testing.T, q Querier) map[int]int {
	t.Helper()
	rows, err := q.Query(context.Background(),
		`SELECT knowledge_id, count(*)::int FROM recall_events GROUP BY knowledge_id`)
	if err != nil {
		t.Fatalf("recall_events 집계: %v", err)
	}
	out := map[int]int{}
	for _, r := range rows {
		id, ok1 := t6CellInt(r[0])
		n, ok2 := t6CellInt(r[1])
		if !ok1 || !ok2 {
			t.Fatalf("집계 셀 변환 불가: %v", r)
		}
		out[id] = n
	}
	return out
}

// t6RankedSnapshot — a snapshot of the derived-ranking query (the T4
// pattern — the same aggregate-join definition as the hot ranking
// query): serializes knowledge id·cnt·last_at into sorted text for the
// both-sides-equal verdict.
func t6RankedSnapshot(t *testing.T, q Querier) string {
	t.Helper()
	rows, err := q.Query(context.Background(),
		`SELECT k.id, re.cnt, re.last_at FROM knowledge k
		 LEFT JOIN (SELECT knowledge_id, count(*)::int AS cnt, max(recalled_at) AS last_at
		           FROM recall_events GROUP BY knowledge_id) re
		 ON re.knowledge_id = k.id
		 WHERE k.topic LIKE 't6dd-%'
		 ORDER BY k.id`)
	if err != nil {
		t.Fatalf("파생 랭킹 조회: %v", err)
	}
	var b strings.Builder
	for _, r := range rows {
		id, _ := t6CellInt(r[0])
		cnt := "NULL"
		if n, ok := t6CellInt(r[1]); ok {
			cnt = fmt.Sprintf("%d", n)
		}
		last := "NULL"
		if ts, ok := r[2].(time.Time); ok {
			last = ts.UTC().Format(time.RFC3339Nano)
		}
		fmt.Fprintf(&b, "%d:%s:%s\n", id, cnt, last)
	}
	return b.String()
}

// t6TopicCount — the count of t6dd synthetic knowledge rows (the
// new-row coexistence verdict).
func t6TopicCount(t *testing.T, q Querier) int {
	t.Helper()
	rows, err := q.Query(context.Background(),
		`SELECT count(*)::int FROM knowledge WHERE topic LIKE 't6dd-%'`)
	if err != nil || len(rows) == 0 {
		t.Fatalf("knowledge 카운트: rows=%v err=%v", rows, err)
	}
	n, ok := t6CellInt(rows[0][0])
	if !ok {
		t.Fatalf("카운트 셀 변환 불가: %v", rows[0])
	}
	return n
}

// TestDualDeviceRecallAppendOnlyRealSlow — the R38 fatal-scenario
// regression (acceptance criteria). ① A commits 3 synthetic knowledge →
// push, B clone+pull ② A·B both independently read (recall-event
// accrual + audit commit) + each writes new knowledge with a commit ③
// A push → B pull (+the reverse B push → A pull) — the whole way no
// panic·recall_events union on both sides·new rows coexisting on both·
// derived rankings identical on both. Slow (FLINT_TEST_DSN).
func TestDualDeviceRecallAppendOnlyRealSlow(t *testing.T) {
	ctx := context.Background()
	dsn := testDSN(t)
	// Scratch creation on the shared server·mutual exclusion with other
	// tests. The unlock must be registered on t — discarding it held the
	// exclusive flock for the rest of the process and stalled every later
	// sharedDBLock caller (~120s, measured on TestMigrateGatesViewReapply).
	t.Cleanup(sharedDBLock(t))
	base, err := Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("Connect base: %v", err)
	}
	// The base connection closes via cleanup (not defer — so that in
	// t.Cleanup LIFO the DROP runs before base. If the DROP goes out over
	// a closed pool, the scratch DB survives on the server).
	t.Cleanup(base.Close)

	suffix := fmt.Sprintf("%d", time.Now().UnixNano()%1e9)
	aName := pgx.Identifier{"t6dd_a_" + suffix}.Sanitize()
	bName := pgx.Identifier{"t6dd_b_" + suffix}.Sanitize()
	t.Cleanup(func() {
		_, _ = base.Exec(ctx, "DROP DATABASE IF EXISTS "+aName)
		_, _ = base.Exec(ctx, "DROP DATABASE IF EXISTS "+bName)
	})
	if _, err := base.Exec(ctx, "CREATE DATABASE "+aName); err != nil {
		t.Fatalf("CREATE DATABASE A: %v", err)
	}
	rawURL := "file://" + filepath.Join(t.TempDir(), "t6dd-remote")

	// A — a flint DB in the migration-complete state.
	repoA, err := NewRepo(ctx, dsnForDatabase(t, dsn, aName))
	if err != nil {
		t.Fatalf("NewRepo A: %v", err)
	}
	defer repoA.Close()
	if err := repoA.Migrate(ctx); err != nil {
		t.Fatalf("Migrate A: %v", err)
	}
	qa := poolQuerier{repoA.pool}
	if err := RemoteAdd(ctx, qa, "origin", rawURL); err != nil {
		t.Fatalf("RemoteAdd A: %v", err)
	}
	// ① Commit 3 synthetic knowledge → push.
	seedTopics := []string{"t6dd-시드-a", "t6dd-시드-b", "t6dd-시드-c"}
	seedRecs := make([]map[string]any, 0, 3)
	for i, topic := range seedTopics {
		seedRecs = append(seedRecs, knowledgeRec(fmt.Sprintf("t6dd-seed-%d-%s", i, suffix)))
		seedRecs[i]["topic"] = topic
	}
	ids, err := repoA.InsertBatch(ctx, seedRecs)
	if err != nil {
		t.Fatalf("InsertBatch A: %v", err)
	}
	t6Commit(t, qa, "t6dd: seed 3 knowledge")
	if _, err := SyncPush(ctx, qa, "origin"); err != nil {
		t.Fatalf("① SyncPush A: %v", err)
	}

	// B — clone from A's remote (the device-addition scenario. clone
	// registers origin automatically — the sync_test measured
	// convention).
	if _, err := base.Exec(ctx, `SELECT dolt_clone($1, $2)`, rawURL, strings.Trim(bName, `"`)); err != nil {
		t.Fatalf("dolt_clone B: %v", err)
	}
	repoB, err := NewRepo(ctx, dsnForDatabase(t, dsn, bName))
	if err != nil {
		t.Fatalf("NewRepo B: %v", err)
	}
	defer repoB.Close()
	if err := repoB.Migrate(ctx); err != nil {
		t.Fatalf("Migrate B(멱등 no-op여야 함): %v", err)
	}
	qb := poolQuerier{repoB.pool}
	if res, err := SyncPull(ctx, qb, "origin", false); err != nil {
		t.Fatalf("① SyncPull B: %v", err)
	} else if res.Status != "up_to_date" {
		t.Fatalf("① clone 직후 pull status = %q, want up_to_date", res.Status)
	}

	// ② Both sides independently read (recall-event accrual + audit
	// commit)·each writes new knowledge. A recalls 2 seeds, B the
	// remaining 1 — the discriminating source of the union verdict.
	if err := repoA.BumpRecallScores(ctx, []int{ids[0], ids[1]}); err != nil {
		t.Fatalf("② BumpRecallScores A: %v", err)
	}
	t6Commit(t, qa, "read: recall bump (t6dd A)")
	aNewRec := knowledgeRec("t6dd-new-A-" + suffix)
	aNewRec["topic"] = "t6dd-new-A-" + suffix
	aNew, err := repoA.InsertBatch(ctx, []map[string]any{aNewRec})
	if err != nil {
		t.Fatalf("② InsertBatch A 신규: %v", err)
	}
	t6Commit(t, qa, "t6dd: A new knowledge")

	if err := repoB.BumpRecallScores(ctx, []int{ids[2]}); err != nil {
		t.Fatalf("② BumpRecallScores B: %v", err)
	}
	t6Commit(t, qb, "read: recall bump (t6dd B)")
	// B's new row is inserted with an explicit id (9000s) — if both
	// sides' sequences split from the same point and the serial default
	// assigns the same id, the PK collision (a non-goal — same-row write
	// conflicts are the existing contract) would pollute the regression
	// measurement. The row contents follow the InsertBatch convention
	// (synthetic demo).
	if _, err := repoB.pool.Exec(ctx,
		`INSERT INTO knowledge (id, topic, summary, tags, domain, confidence, origin, status, content_hash)
		 VALUES (9001, $1, '요약', ':t6dd:', 'main', 0.9, 'agent', 'active', $2)`,
		"t6dd-new-B-"+suffix, "t6dd-new-b-hash-"+suffix); err != nil {
		t.Fatalf("② B 신규 지식 삽입: %v", err)
	}
	t6Commit(t, qb, "t6dd: B new knowledge")

	// ③ A push → B pull (the fatal scenario's verdict point) → the
	// reverse once.
	if _, err := SyncPush(ctx, qa, "origin"); err != nil {
		t.Fatalf("③ SyncPush A: %v", err)
	}
	t6NoPanic(t, "③ SyncPull B(A push 병합)", func() error { _, e := SyncPull(ctx, qb, "origin", false); return e }())
	if _, err := SyncPush(ctx, qb, "origin"); err != nil {
		t.Fatalf("③ SyncPush B(역방향): %v", err)
	}
	t6NoPanic(t, "③ SyncPull A(B push 병합)", func() error { _, e := SyncPull(ctx, qa, "origin", false); return e }())

	// Assertion 1 — recall_events union on both sides: the 2 A recalled +
	// the 1 B recalled exist in both DBs (3 events·each of the 3
	// knowledge ids once).
	countsA := t6RecallCounts(t, qa)
	countsB := t6RecallCounts(t, qb)
	want := map[int]int{ids[0]: 1, ids[1]: 1, ids[2]: 1}
	for _, side := range []struct {
		name   string
		counts map[int]int
	}{{"A", countsA}, {"B", countsB}} {
		for id, n := range want {
			if side.counts[id] != n {
				t.Errorf("recall_events(%s) id=%d = %d건, want %d(union 미달)", side.name, id, side.counts[id], n)
			}
		}
		if len(side.counts) != len(want) {
			t.Errorf("recall_events(%s) 지식 수 = %d, want %d(예상 외 잔존: %v)", side.name, len(side.counts), len(want), side.counts)
		}
	}

	// Assertion 2 — knowledge new rows coexisting on both sides: 3 seeds
	// + 1 A-new + 1 B-new = 5.
	for _, side := range []struct {
		name string
		q    Querier
	}{{"A", qa}, {"B", qb}} {
		if n := t6TopicCount(t, side.q); n != 5 {
			t.Errorf("knowledge(%s) t6dd 행 = %d, want 5(신규 행 공존 미달)", side.name, n)
		}
	}

	// Assertion 3 — the derived-ranking query yields identical results on
	// both sides (the T4 aggregate-join definition).
	snapA, snapB := t6RankedSnapshot(t, qa), t6RankedSnapshot(t, qb)
	if snapA != snapB {
		t.Errorf("파생 랭킹 스냅숏 불일치:\nA:\n%s\nB:\n%s", snapA, snapB)
	}

	// Assertion 4 — A's new-row id also exists on B (coexistence
	// detail).
	rows, err := qb.Query(ctx, `SELECT count(*)::int FROM knowledge WHERE id = $1`, aNew[0])
	if err != nil || len(rows) == 0 {
		t.Fatalf("B에서 A 신규 행 조회: %v", err)
	}
	if n, _ := t6CellInt(rows[0][0]); n != 1 {
		t.Errorf("B에 A 신규 행(id=%d) = %d건, want 1", aNew[0], n)
	}
	t.Logf("t6dd 이중 기기 회귀: recall_events union 3이벤트(지식 3개)·t6dd knowledge 5행 양측 공존·파생 랭킹 스냅숏 일치(%d행)", strings.Count(snapA, "\n"))
}

// t6CellInt — lenient numeric cell conversion (int32/int64/int — the
// measured count(*)::int type).
func t6CellInt(v any) (int, bool) {
	switch n := v.(type) {
	case int32:
		return int(n), true
	case int64:
		return int(n), true
	case int:
		return n, true
	}
	return 0, false
}
