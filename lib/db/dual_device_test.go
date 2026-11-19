// dual_device_test.go — v1.9.0 T6 이중 기기 회귀(수용 기준 — 스펙 요구 6).
// R38 치명 시나리오(양측 working set에 read 페이로드 → pull 병합 패닉/
// 동일 knowledge 행 merge 불능)를 recall append-only(스키마 v12) 전환 후
// 재현해 무패닉·이벤트 union·신규 행 공존·파생 랭킹 일치를 실측한다.
// 격리 DB A·B(scratch 생성·말미 DROP), file:// 원격(R34 격리 관례·
// TestSyncPushPullRoundTripRealSlow 패턴 승계), sharedDBLock 상호배제.
// 합성 데모 데이터만 사용(개인 데이터 금지).
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

// t6Commit — scratch DB 전체 스테이징 커밋(테스트 소유 하네스 — flint
// 런타임의 -A 폐지 계약과 무관, sync_test 관례 승계). 마이그레이션이 만든
// 전 스키마(테이블·뷰·시퀀스)를 원격에 실으려면 초기 커밋은 -A여야 한다.
func t6Commit(t *testing.T, q Querier, msg string) {
	t.Helper()
	if _, err := q.Query(context.Background(), `SELECT dolt_add('-A')`); err != nil {
		t.Fatalf("dolt_add: %v", err)
	}
	if _, err := q.Query(context.Background(), `SELECT dolt_commit('-m', $1)`, msg); err != nil {
		t.Fatalf("dolt_commit(%q): %v", msg, err)
	}
}

// t6NoPanic — 원격 연산 오류가 엔진 패닉(v1.7.2 S1 완화 대상 —
// "caught panic" 감지 문자·ErrRemotePanic·XX000)이 아님을 단언. R38
// 치명 회귀의 핵심 판정: 완화 안내(RemotePanicGuide)가 뜨면 이미 실패.
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

// t6RecallCounts — 지식 id → 회상 이벤트 수(파생 랭킹의 원천 집계와 같은
// 정의). union·랭킹 일치 판정의 재료.
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

// t6RankedSnapshot — 파생 랭킹 조회(T4 패턴 — hot 랭킹 질의의 집계 조인과
// 동일 정의)의 스냅숏: 지식 id·cnt·last_at을 정렬된 텍스트로 직렬화해
// 양측 동일 결과 판정에 쓴다.
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

// t6TopicCount — t6dd 합성 지식 행 수(신규 행 공존 판정).
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

// TestDualDeviceRecallAppendOnlyRealSlow — R38 치명 시나리오 회귀(수용
// 기준). ① A 합성 지식 3건 커밋→push, B clone+pull ② A·B 양측 독립
// read(회상 이벤트 적립+감사 커밋)+각기 신규 지식 쓰기 커밋 ③ A push→
// B pull(+역방향 B push→A pull) — 전 과정 무패닉·recall_events 양측
// union·신규 행 양측 공존·파생 랭킹 양측 동일. slow(FLINT_TEST_DSN).
func TestDualDeviceRecallAppendOnlyRealSlow(t *testing.T) {
	ctx := context.Background()
	dsn := testDSN(t)
	_ = sharedDBLock(t) // 공유 서버 scratch 생성·타 테스트와 상호배제
	base, err := Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("Connect base: %v", err)
	}
	// base 연결은 cleanup으로 닫는다(defer가 아니라 — t.Cleanup LIFO에서
	// DROP이 base보다 먼저 실행되도록. DROP이 닫힌 풀로 나가면 scratch
	// DB가 서버에 잔존한다).
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

	// A — 마이그레이션 완료 상태의 flint DB.
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
	// ① 합성 지식 3건 커밋 → push.
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

	// B — A 원격에서 clone(기기 추가 시나리오. clone이 origin을 자동
	// 등록한다 — sync_test 실측 관례).
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

	// ② 양측 독립 read(회상 이벤트 적립 + 감사 커밋)·각기 신규 지식 쓰기.
	// A는 시드 2건 회상, B는 나머지 1건 회상 — union 판정의 변별 원천.
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
	// B 신규 행은 명시 id(9000대)로 삽입 — 양측 시퀀스가 같은 지점에서
	// 갈라져 serial 기본값이 같은 id를 배정하면 PK 충돌(비목표 — 동일 행
	// 쓰기 충돌은 기존 계약)이 회귀 측정을 오염시킨다. 행 내용은 InsertBatch
	// 관례와 동일(합성 데모).
	if _, err := repoB.pool.Exec(ctx,
		`INSERT INTO knowledge (id, topic, summary, tags, domain, confidence, origin, status, content_hash)
		 VALUES (9001, $1, '요약', ':t6dd:', 'main', 0.9, 'agent', 'active', $2)`,
		"t6dd-new-B-"+suffix, "t6dd-new-b-hash-"+suffix); err != nil {
		t.Fatalf("② B 신규 지식 삽입: %v", err)
	}
	t6Commit(t, qb, "t6dd: B new knowledge")

	// ③ A push → B pull(치명 시나리오의 판정 지점) → 역방향 1회.
	if _, err := SyncPush(ctx, qa, "origin"); err != nil {
		t.Fatalf("③ SyncPush A: %v", err)
	}
	t6NoPanic(t, "③ SyncPull B(A push 병합)", func() error { _, e := SyncPull(ctx, qb, "origin", false); return e }())
	if _, err := SyncPush(ctx, qb, "origin"); err != nil {
		t.Fatalf("③ SyncPush B(역방향): %v", err)
	}
	t6NoPanic(t, "③ SyncPull A(B push 병합)", func() error { _, e := SyncPull(ctx, qa, "origin", false); return e }())

	// 단언 1 — recall_events 양측 union: A가 회상한 2건 + B가 회상한 1건이
	// 양쪽 DB 모두에 존재(이벤트 3건·지식 3개 id 각 1회).
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

	// 단언 2 — knowledge 신규 행 양측 공존: 시드 3 + A 신규 1 + B 신규 1 = 5.
	for _, side := range []struct {
		name string
		q    Querier
	}{{"A", qa}, {"B", qb}} {
		if n := t6TopicCount(t, side.q); n != 5 {
			t.Errorf("knowledge(%s) t6dd 행 = %d, want 5(신규 행 공존 미달)", side.name, n)
		}
	}

	// 단언 3 — 파생 랭킹 조회 양측 동일 결과(T4 집계 조인 동일 정의).
	snapA, snapB := t6RankedSnapshot(t, qa), t6RankedSnapshot(t, qb)
	if snapA != snapB {
		t.Errorf("파생 랭킹 스냅숏 불일치:\nA:\n%s\nB:\n%s", snapA, snapB)
	}

	// 단언 4 — A 신규 행 id가 B에도 존재(공존 상세).
	rows, err := qb.Query(ctx, `SELECT count(*)::int FROM knowledge WHERE id = $1`, aNew[0])
	if err != nil || len(rows) == 0 {
		t.Fatalf("B에서 A 신규 행 조회: %v", err)
	}
	if n, _ := t6CellInt(rows[0][0]); n != 1 {
		t.Errorf("B에 A 신규 행(id=%d) = %d건, want 1", aNew[0], n)
	}
	t.Logf("t6dd 이중 기기 회귀: recall_events union 3이벤트(지식 3개)·t6dd knowledge 5행 양측 공존·파생 랭킹 스냅숏 일치(%d행)", strings.Count(snapA, "\n"))
}

// t6CellInt — pgx 수치 셀 관용 변환(int32/int64/int — count(*)::int 실측형).
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
