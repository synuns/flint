//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/synuns/flint/lib/db"
)

func latestCommitHash(t *testing.T, bin string, env []string, session string) string {
	t.Helper()
	out, code := runFlint(t, bin, env, []string{"audit", "--session", session}, "")
	if code != 0 {
		t.Fatalf("audit(%s) exit=%d, want 0; out=%s", session, code, out)
	}
	m := decode(t, out)
	log, _ := m["log"].([]any)
	if len(log) == 0 {
		t.Fatalf("audit(%s) log 0행, want >=1; out=%s", session, out)
	}
	e, _ := log[0].(map[string]any)
	hash, _ := e["commit_hash"].(string)
	if hash == "" {
		t.Fatalf("audit(%s) commit_hash 비어 있음; out=%s", session, out)
	}
	return hash
}

func asOfHash(t *testing.T, m map[string]any) string {
	t.Helper()
	ao, _ := m["as_of"].(map[string]any)
	if ao == nil {
		t.Fatalf("as_of 필드 없음; out=%v", m)
	}
	hash, _ := ao["hash"].(string)
	if hash == "" {
		t.Fatalf("as_of.hash 비어 있음; out=%v", ao)
	}
	return hash
}

func TestV08LifecycleChain(t *testing.T) {

	repo, err := db.NewRepo(context.Background(), dsn())
	if err != nil {
		t.Skipf("Doltgres 미기동: %v", err)
	}
	t.Cleanup(repo.Close)
	if err := repo.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	pool := mustDB(t)

	cleanupTopics(t, pool, "e2e-v08-")
	runPrefix := fmt.Sprintf("e2e-v08-%d-", time.Now().UnixNano())
	t.Cleanup(func() { cleanupTopics(t, pool, runPrefix) })

	topicA, topicB := runPrefix+"k1", runPrefix+"k2"
	sessA, sessB := runPrefix+"s1", runPrefix+"s2"

	env := flintEnv(
		"FLINT_CONFIG_DIR="+t.TempDir(),
		"FLINT_CLAUDE_HOME="+t.TempDir(),
		"FLINT_DSN="+dsn(),
	)
	bin := rebuildBin(t)

	var basePending int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM knowledge WHERE status = 'pending' AND invalid_at IS NULL`).
		Scan(&basePending); err != nil {
		t.Fatalf("baseline pending 수 실패: %v", err)
	}

	out, code := runFlint(t, bin, env,
		[]string{"write", "knowledge", "--session", sessA},
		fmt.Sprintf(`{"topic":%q,"summary":"v0.8 체인 프로브 1","confidence":0.9}`, topicA))
	if code != 0 {
		t.Fatalf("① write exit=%d, want 0; out=%s", code, out)
	}
	wm := decode(t, out)
	if wm["status"] != "committed" {
		t.Fatalf("① write status=%v, want committed; out=%s", wm["status"], out)
	}
	oldID := int(wm["id"].(float64))
	commitA := latestCommitHash(t, bin, env, sessA)

	out, code = runFlint(t, bin, env,
		[]string{"write", "knowledge", "--session", sessB},
		fmt.Sprintf(`{"topic":%q,"summary":"v0.8 체인 프로브 2","confidence":0.9}`, topicB))
	if code != 0 {
		t.Fatalf("② write exit=%d, want 0; out=%s", code, out)
	}
	wm2 := decode(t, out)
	newID := int(wm2["id"].(float64))
	if newID == oldID {
		t.Fatalf("② id=%d, ①과 동일(별도 행 아님)", newID)
	}
	commitB := latestCommitHash(t, bin, env, sessB)

	out, code = runFlint(t, bin, env,
		[]string{"read", runPrefix, "--as-of", commitA, "--json", "--fields", "topic"}, "")
	if code != 0 {
		t.Fatalf("③ read --as-of exit=%d, want 0; out=%s", code, out)
	}

	var rows []map[string]any
	var asOfMeta map[string]any
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		if l == "" {
			continue
		}
		if strings.HasPrefix(l, `{"as_of":`) {
			asOfMeta = decode(t, l)
			continue
		}
		rows = append(rows, decode(t, l))
	}
	if len(rows) != 1 || rows[0]["topic"] != topicA {
		t.Fatalf("③ as-of 결과 %d행(%v), want 1행(k1만); out=%s", len(rows), rows, out)
	}
	if got := asOfHash(t, asOfMeta); got != commitA {
		t.Fatalf("③ as_of.hash=%q, want %q", got, commitA)
	}

	out, code = runFlint(t, bin, env, []string{"diff", commitA, commitB}, "")
	if code != 0 {
		t.Fatalf("④ diff exit=%d, want 0; out=%s", code, out)
	}
	dm := decode(t, out)
	if dm["from"] != commitA || dm["to"] != commitB {
		t.Fatalf("④ from/to=%v/%v, want 정규화 해시; out=%s", dm["from"], dm["to"], out)
	}
	if dm["added"] != float64(1) {
		t.Fatalf("④ added=%v, want 1; out=%s", dm["added"], out)
	}

	out, code = runFlint(t, bin, env, []string{"status", "--as-of", commitA}, "")
	if code != 0 {
		t.Fatalf("⑤ status --as-of exit=%d, want 0; out=%s", code, out)
	}
	sm := decode(t, out)
	if sm["pending"] != float64(basePending) {
		t.Fatalf("⑤ pending=%v, want %d(시작 baseline — 공유 DB 잔존 pending과 무관); out=%s",
			sm["pending"], basePending, out)
	}
	if got := asOfHash(t, sm); got != commitA {
		t.Fatalf("⑤ as_of.hash=%q, want %q", got, commitA)
	}

	out, code = runFlint(t, bin, env,
		[]string{"maintain", "--supersede", strconv.Itoa(oldID), "--by", strconv.Itoa(newID)}, "")
	if code != 0 {
		t.Fatalf("⑥ supersede exit=%d, want 0; out=%s", code, out)
	}
	pm := decode(t, out)

	if pm["status"] != "superseded" {
		t.Fatalf("⑥ status=%v, want superseded; out=%s", pm["status"], out)
	}
	if pm["old"] != float64(oldID) {
		t.Fatalf("⑥ old=%v, want %d; out=%s", pm["old"], oldID, out)
	}
	if pm["superseded_by"] != float64(newID) {
		t.Fatalf("⑥ superseded_by=%v, want %d; out=%s", pm["superseded_by"], newID, out)
	}

	out, code = runFlint(t, bin, env, []string{"maintain", "--invalidate-stale"}, "")
	if code != 0 {
		t.Fatalf("⑦ invalidate-stale exit=%d, want 0(dry-run 기본); out=%s", code, out)
	}
	decode(t, out)
	if !strings.Contains(out, "to invalidate") {
		t.Fatalf("⑦ 요약행('N to invalidate') 없음; out=%s", out)
	}

	out, code = runFlint(t, bin, env, []string{"audit", "--stat"}, "")
	if code != 0 {
		t.Fatalf("⑧ audit --stat exit=%d, want 0; out=%s", code, out)
	}
	am := decode(t, out)
	log, _ := am["log"].([]any)
	if len(log) == 0 {
		t.Fatalf("⑧ audit log 0행; out=%s", out)
	}
	if _, ok := am["stat"]; !ok {
		e, _ := log[0].(map[string]any)
		if _, ok := e["stat"]; !ok {
			t.Fatalf("⑧ stat 필드 없음(상위·항목 모두); out=%s", out)
		}
	}
}
