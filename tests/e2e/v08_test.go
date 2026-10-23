//go:build e2e

// v08_test.go — T90 v0.8 수명주기+시간여행 체인 e2e(실DB·순차·전 구간
// bin/flint 서브프로세스). write→audit 해시 획득→read --as-of→diff→
// status --as-of→maintain --supersede→--invalidate-stale dry-run→audit --stat
// 8단계가 한 줄로 이어지는지 검증한다. Review Focus: 순서 의존·해시 획득
// 경로(audit 출력 파싱 — --session 필터로 커밋 귀속 확정)·환경 격리(임시
// FLINT_CONFIG_DIR·FLINT_CLAUDE_HOME — v07 flintEnv 패턴 재사용).
// 실행: go test -tags e2e ./tests/e2e/ -run TestV08LifecycleChain -count=1
package e2e

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"flint/lib/db"
)

// latestCommitHash — audit --session 출력 파싱으로 해당 세션 커밋의 해시
// 확보(체인의 유일한 해시 획득 경로. 동시 커밋 혼입 차단).
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

// asOfHash — read/status --as-of 결과의 as_of.hash 정합 확인 공용.
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

// TestV08LifecycleChain — 8단계 순서 의존 체인. state: 스키마 v5
// superseded_by·invalid_at 갱신. protocol: as-of 정규화(해시)·dry-run 기본.
func TestV08LifecycleChain(t *testing.T) {
	// 신규 DB 대응 — 테이블·뷰 확보(v07 패턴). 스키마 v5 승격은 첫 접근
	// 시 자동(T82 배선).
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
	t.Cleanup(func() { cleanupTopics(t, pool, "e2e-v08-") })

	// 환경 격리 — 설정·플러그인 캐시를 임시 디렉터리로.
	env := flintEnv(
		"FLINT_CONFIG_DIR="+t.TempDir(),
		"FLINT_CLAUDE_HOME="+t.TempDir(),
		"FLINT_DSN="+dsn(),
	)
	bin := rebuildBin(t)

	// ① write 1건(confidence 0.9 → active) — 커밋 A 확보.
	out, code := runFlint(t, bin, env,
		[]string{"write", "knowledge", "--session", "e2e-v08-s1"},
		`{"topic":"e2e-v08-k1","summary":"v0.8 체인 프로브 1","confidence":0.9}`)
	if code != 0 {
		t.Fatalf("① write exit=%d, want 0; out=%s", code, out)
	}
	wm := decode(t, out)
	if wm["status"] != "committed" {
		t.Fatalf("① write status=%v, want committed; out=%s", wm["status"], out)
	}
	oldID := int(wm["id"].(float64))
	commitA := latestCommitHash(t, bin, env, "e2e-v08-s1")

	// ② write 1건 더 — 커밋 B.
	out, code = runFlint(t, bin, env,
		[]string{"write", "knowledge", "--session", "e2e-v08-s2"},
		`{"topic":"e2e-v08-k2","summary":"v0.8 체인 프로브 2","confidence":0.9}`)
	if code != 0 {
		t.Fatalf("② write exit=%d, want 0; out=%s", code, out)
	}
	wm2 := decode(t, out)
	newID := int(wm2["id"].(float64))
	if newID == oldID {
		t.Fatalf("② id=%d, ①과 동일(별도 행 아님)", newID)
	}
	commitB := latestCommitHash(t, bin, env, "e2e-v08-s2")

	// ③ read --as-of A — 두 토픽 질의로 과거 시점엔 첫 건만 보이는지.
	out, code = runFlint(t, bin, env,
		[]string{"read", "e2e-v08-", "--as-of", commitA, "--json", "--fields", "topic"}, "")
	if code != 0 {
		t.Fatalf("③ read --as-of exit=%d, want 0; out=%s", code, out)
	}
	var rows []map[string]any
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		if l != "" {
			rows = append(rows, decode(t, l))
		}
	}
	if len(rows) != 1 || rows[0]["topic"] != "e2e-v08-k1" {
		t.Fatalf("③ as-of 결과 %d행(%v), want 1행(k1만); out=%s", len(rows), rows, out)
	}
	if got := asOfHash(t, rows[0]); got != commitA {
		t.Fatalf("③ as_of.hash=%q, want %q", got, commitA)
	}

	// ④ diff A B — 두 커밋 사이 행 추가는 정확히 1.
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

	// ⑤ status --as-of A — 커밋 A 시점 pending 0·as_of 해시 정합.
	out, code = runFlint(t, bin, env, []string{"status", "--as-of", commitA}, "")
	if code != 0 {
		t.Fatalf("⑤ status --as-of exit=%d, want 0; out=%s", code, out)
	}
	sm := decode(t, out)
	if sm["pending"] != float64(0) {
		t.Fatalf("⑤ pending=%v, want 0(시점 스냅샷); out=%s", sm["pending"], out)
	}
	if got := asOfHash(t, sm); got != commitA {
		t.Fatalf("⑤ as_of.hash=%q, want %q", got, commitA)
	}

	// ⑥ supersede — OLD k1 invalidated·superseded_by=NEW.
	out, code = runFlint(t, bin, env,
		[]string{"maintain", "--supersede", strconv.Itoa(oldID), "--by", strconv.Itoa(newID)}, "")
	if code != 0 {
		t.Fatalf("⑥ supersede exit=%d, want 0; out=%s", code, out)
	}
	pm := decode(t, out)
	if pm["status"] != "invalidated" {
		t.Fatalf("⑥ status=%v, want invalidated; out=%s", pm["status"], out)
	}
	if pm["superseded_by"] != float64(newID) {
		t.Fatalf("⑥ superseded_by=%v, want %d; out=%s", pm["superseded_by"], newID, out)
	}

	// ⑦ invalidate-stale dry-run — 대상·요약만, exit 0(쓰기 없음).
	out, code = runFlint(t, bin, env, []string{"maintain", "--invalidate-stale"}, "")
	if code != 0 {
		t.Fatalf("⑦ invalidate-stale exit=%d, want 0(dry-run 기본); out=%s", code, out)
	}
	decode(t, out) // 한 줄 JSON 유지
	if !strings.Contains(out, "to invalidate") {
		t.Fatalf("⑦ 요약행('N to invalidate') 없음; out=%s", out)
	}

	// ⑧ audit --stat — 커밋 목록에 stat 요약 필드 부록.
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
