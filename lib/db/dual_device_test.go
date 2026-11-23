package db

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/synuns/flint/internal/testengine"
)

func t6Commit(t *testing.T, q Querier, msg string) {
	t.Helper()
	if _, err := q.Query(context.Background(), `SELECT dolt_add('-A')`); err != nil {
		t.Fatalf("dolt_add: %v", err)
	}
	if _, err := q.Query(context.Background(), `SELECT dolt_commit('-m', $1)`, msg); err != nil {
		t.Fatalf("dolt_commit(%q): %v", msg, err)
	}
}

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

func TestDualDeviceRecallAppendOnlyRealSlow(t *testing.T) {
	ctx := context.Background()
	dsn := testDSN(t)

	t.Cleanup(sharedDBLock(t))
	base, err := Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("Connect base: %v", err)
	}

	t.Cleanup(base.Close)

	suffix := fmt.Sprintf("%d", time.Now().UnixNano()%1e9)
	aDSN := testengine.Scratch(t)
	bName, bDSN := testengine.CloneTarget(t)
	rawURL := "file://" + filepath.Join(t.TempDir(), "t6dd-remote")

	repoA, err := NewRepo(ctx, aDSN)
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

	if _, err := base.Exec(ctx, `SELECT dolt_clone($1, $2)`, rawURL, bName); err != nil {
		t.Fatalf("dolt_clone B: %v", err)
	}
	repoB, err := NewRepo(ctx, bDSN)
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

	if _, err := repoB.pool.Exec(ctx,
		`INSERT INTO knowledge (id, topic, summary, tags, domain, confidence, origin, status, content_hash)
		 VALUES (9001, $1, '요약', ':t6dd:', 'main', 0.9, 'agent', 'active', $2)`,
		"t6dd-new-B-"+suffix, "t6dd-new-b-hash-"+suffix); err != nil {
		t.Fatalf("② B 신규 지식 삽입: %v", err)
	}
	t6Commit(t, qb, "t6dd: B new knowledge")

	if _, err := SyncPush(ctx, qa, "origin"); err != nil {
		t.Fatalf("③ SyncPush A: %v", err)
	}
	t6NoPanic(t, "③ SyncPull B(A push 병합)", func() error { _, e := SyncPull(ctx, qb, "origin", false); return e }())
	if _, err := SyncPush(ctx, qb, "origin"); err != nil {
		t.Fatalf("③ SyncPush B(역방향): %v", err)
	}
	t6NoPanic(t, "③ SyncPull A(B push 병합)", func() error { _, e := SyncPull(ctx, qa, "origin", false); return e }())

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

	for _, side := range []struct {
		name string
		q    Querier
	}{{"A", qa}, {"B", qb}} {
		if n := t6TopicCount(t, side.q); n != 5 {
			t.Errorf("knowledge(%s) t6dd 행 = %d, want 5(신규 행 공존 미달)", side.name, n)
		}
	}

	snapA, snapB := t6RankedSnapshot(t, qa), t6RankedSnapshot(t, qb)
	if snapA != snapB {
		t.Errorf("파생 랭킹 스냅숏 불일치:\nA:\n%s\nB:\n%s", snapA, snapB)
	}

	rows, err := qb.Query(ctx, `SELECT count(*)::int FROM knowledge WHERE id = $1`, aNew[0])
	if err != nil || len(rows) == 0 {
		t.Fatalf("B에서 A 신규 행 조회: %v", err)
	}
	if n, _ := t6CellInt(rows[0][0]); n != 1 {
		t.Errorf("B에 A 신규 행(id=%d) = %d건, want 1", aNew[0], n)
	}
	t.Logf("t6dd 이중 기기 회귀: recall_events union 3이벤트(지식 3개)·t6dd knowledge 5행 양측 공존·파생 랭킹 스냅숏 일치(%d행)", strings.Count(snapA, "\n"))
}

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
