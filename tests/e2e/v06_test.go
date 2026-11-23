//go:build e2e

package e2e

import (
	"context"
	"testing"
	"time"

	"github.com/synuns/flint/lib/db"
)

func TestV06VisibilityChain(t *testing.T) {

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

	if _, code := runCLI(t, []string{"read", "e2e-v06-k"}, ""); code != 0 {
		t.Fatalf("read exit=%d, want 0", code)
	}
	if _, code := runCLI(t, []string{"hot"}, ""); code != 0 {
		t.Fatalf("hot exit=%d, want 0", code)
	}

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

	out, code = runCLI(t, []string{"doctor", "--check", "db"}, "")
	if code != 0 {
		t.Fatalf("doctor --check db exit=%d, want 0; out=%s", code, out)
	}
	if dm := decode(t, out); dm["status"] != "ok" {
		t.Fatalf("doctor db status=%v, want ok; out=%s", dm["status"], out)
	}

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

	out, code = runCLI(t, []string{"write", "knowledge", "--source", "bad"},
		`{"topic":"e2e-v06-bad","summary":"무효 소스","confidence":0.9}`)
	if code != 3 {
		t.Fatalf("무효 --source exit=%d, want 3; out=%s", code, out)
	}
}
