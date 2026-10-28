//go:build e2e

// v06_test.go — T70 v0.6 적립 가시성 체인 e2e(실DB·순차). 유입 3경로 계측
// (write --source·read·hot)이 meta last_*·daily에 쌓이고 status·report·doctor·
// maintain --stale·재read 증분까지 한 줄로 이어지는지 검증한다. 병렬 회귀는
// 기존 parallel_test.go(T50) 소관 — 여기는 순차 시나리오만 다룬다.
// 실행: go test -tags e2e ./tests/e2e/ -run TestV06VisibilityChain -count=1
package e2e

import (
	"context"
	"testing"
	"time"

	"flint/lib/db"
)

// TestV06VisibilityChain — Review Focus(state): 전 체인 end-to-end·재read 증분.
// protocol: exit 코드 계약(무효 --source 3 포함).
func TestV06VisibilityChain(t *testing.T) {
	// 신규 DB(전용 컨테이너) 대응 — mustDB의 뷰 재생성에 앞서 테이블부터
	// 확보한다(Migrate 멱등 — 기존 DB에서는 no-op에 가깝다).
	repo, err := db.NewRepo(context.Background(), dsn())
	if err != nil {
		t.Skipf("Doltgres 미기동: %v", err)
	}
	t.Cleanup(repo.Close)
	if err := repo.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	pool := mustDB(t)
	cleanupTopics(t, pool, "e2e-v06-")
	t.Cleanup(func() { cleanupTopics(t, pool, "e2e-v06-") })

	// never_read 검증용 시드 — 91일 전 수집 항목(테스트 전용 직접 INSERT/UPDATE.
	// 제품 write 경로가 아니며, maintain --stale의 read_count=0 AND ingested_at
	// 조건을 결정적으로 만든다).
	seedTopic := "e2e-v06-seed"
	out, code := runCLI(t, []string{"write", "knowledge"},
		`{"topic":"`+seedTopic+`","summary":"91일 전 시드","confidence":0.9}`)
	if code != 0 {
		t.Fatalf("시드 write exit=%d, want 0; out=%s", code, out)
	}
	var seedID int
	if err := pool.QueryRow(context.Background(),
		`SELECT id FROM knowledge WHERE topic = $1`, seedTopic).Scan(&seedID); err != nil {
		t.Fatalf("시드 id 조회: %v", err)
	}
	if _, err := pool.Exec(context.Background(),
		`UPDATE knowledge SET ingested_at = now() - interval '91 days' WHERE id = $1`, seedID); err != nil {
		t.Fatalf("시드 ingested_at 소급: %v", err)
	}

	// write --source hook 2건 — 동일 payload 1회 → duplicate 1(exit 0).
	// duplicate 응답은 기존 행 id 노출 계약(R3): {"status":"duplicate","id":N} —
	// 1회차 커밋 행 id와 정합 비교로 멱등까지 함께 검증한다.
	payload := `{"topic":"e2e-v06-k","summary":"v0.6 체인 프로브","confidence":0.9}`
	out, code = runCLI(t, []string{"write", "knowledge", "--source", "hook"}, payload)
	if code != 0 {
		t.Fatalf("write 1회차 exit=%d, want 0; out=%s", code, out)
	}
	m := decode(t, out)
	if m["status"] != "committed" {
		t.Fatalf("write 1회차 status=%v, want committed; out=%s", m["status"], out)
	}
	firstID, _ := m["id"].(float64)
	out, code = runCLI(t, []string{"write", "knowledge", "--source", "hook"}, payload)
	if code != 0 {
		t.Fatalf("write 2회차(duplicate) exit=%d, want 0; out=%s", code, out)
	}
	dm := decode(t, out)
	if dm["status"] != "duplicate" {
		t.Fatalf("write 2회차 status=%v, want duplicate; out=%s", dm["status"], out)
	}
	if firstID == 0 || dm["id"] != firstID {
		t.Fatalf("write 2회차 id=%v, want %v(1회차 커밋 행 id); out=%s", dm["id"], firstID, out)
	}

	// read → hot: 유입 3경로 완결.
	if _, code := runCLI(t, []string{"read", "e2e-v06-k"}, ""); code != 0 {
		t.Fatalf("read exit=%d, want 0", code)
	}
	if _, code := runCLI(t, []string{"hot"}, ""); code != 0 {
		t.Fatalf("hot exit=%d, want 0", code)
	}

	// status — last_* 3필드 전부 null 아님.
	out, code = runCLI(t, []string{"status"}, "")
	if code != 0 {
		t.Fatalf("status exit=%d, want 0; out=%s", code, out)
	}
	sm := decode(t, out)
	for _, key := range []string{"last_write_at", "last_hot_at", "last_read_at"} {
		v, ok := sm[key].(string)
		if !ok || v == "" {
			t.Fatalf("status %s=%v, want RFC3339 문자열; out=%s", key, sm[key], out)
		}
		if _, err := time.Parse(time.RFC3339, v); err != nil {
			t.Fatalf("status %s=%q가 RFC3339 아님: %v", key, v, err)
		}
	}

	// report --days 7 — 오늘(days 마지막 행) write≥2·hook≥2·dup≥1·hot·read≥1.
	out, code = runCLI(t, []string{"report", "--days", "7"}, "")
	if code != 0 {
		t.Fatalf("report exit=%d, want 0; out=%s", code, out)
	}
	rm := decode(t, out)
	days, _ := rm["days"].([]any)
	if len(days) != 7 {
		t.Fatalf("report days %d행, want 7; out=%s", len(days), out)
	}
	today, _ := days[len(days)-1].(map[string]any)
	num := func(k string) float64 {
		n, _ := today[k].(float64)
		return n
	}
	if num("write") < 2 {
		t.Fatalf("오늘 write=%v, want >=2(committed+duplicate 각 1); out=%s", today["write"], out)
	}
	if num("hook") < 2 {
		t.Fatalf("오늘 hook=%v, want >=2; out=%s", today["hook"], out)
	}
	if num("dup") < 1 {
		t.Fatalf("오늘 dup=%v, want >=1; out=%s", today["dup"], out)
	}
	if num("hot") < 1 || num("read") < 1 {
		t.Fatalf("오늘 hot=%v read=%v, want 각 >=1; out=%s", today["hot"], today["read"], out)
	}

	// doctor --check db — exit 0.
	out, code = runCLI(t, []string{"doctor", "--check", "db"}, "")
	if code != 0 {
		t.Fatalf("doctor --check db exit=%d, want 0; out=%s", code, out)
	}
	if dm := decode(t, out); dm["status"] != "ok" {
		t.Fatalf("doctor db status=%v, want ok; out=%s", dm["status"], out)
	}

	// maintain --stale — never_read 시드 1건만(신규 행은 오늘 수집이라 제외).
	out, code = runCLI(t, []string{"maintain", "--stale"}, "")
	if code != 0 {
		t.Fatalf("maintain --stale exit=%d, want 0(보고 전용); out=%s", code, out)
	}
	mm := decode(t, out)
	if mm["count"] != float64(1) {
		t.Fatalf("stale count=%v, want 1(91일 시드만); out=%s", mm["count"], out)
	}
	items, _ := mm["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("stale items %d건, want 1; out=%s", len(items), out)
	}
	item, _ := items[0].(map[string]any)
	if item["id"] != float64(seedID) || item["never_read"] != true {
		t.Fatalf("stale 항목=%v, want id=%d never_read=true", item, seedID)
	}

	// 재read 후 시드 read_count 증가 확인(직접 쿼리 — BumpReads 실측).
	var before int
	if err := pool.QueryRow(context.Background(),
		`SELECT read_count FROM knowledge WHERE id = $1`, seedID).Scan(&before); err != nil {
		t.Fatalf("시드 read_count 조회: %v", err)
	}
	if before != 0 {
		t.Fatalf("시드 read_count=%d, want 0(재read 전)", before)
	}
	if _, code := runCLI(t, []string{"read", seedTopic}, ""); code != 0 {
		t.Fatalf("재read exit=%d, want 0", code)
	}
	var after int
	if err := pool.QueryRow(context.Background(),
		`SELECT read_count FROM knowledge WHERE id = $1`, seedID).Scan(&after); err != nil {
		t.Fatalf("시드 read_count 재조회: %v", err)
	}
	if after <= before {
		t.Fatalf("재read 후 read_count=%d, want >%d(증분)", after, before)
	}

	// protocol: 무효 --source는 exit 3.
	out, code = runCLI(t, []string{"write", "knowledge", "--source", "bad"},
		`{"topic":"e2e-v06-bad","summary":"무효 소스","confidence":0.9}`)
	if code != 3 {
		t.Fatalf("무효 --source exit=%d, want 3; out=%s", code, out)
	}
}
