//go:build e2e

// v07_test.go — T79 v0.7 적립 자동화 체인 e2e(실DB·순차·전 구간 bin/flint
// 서브프로세스). compact(보존)→report 계측→마커→nudge(증류 넛지)→--clear
// 소멸→재넛지 무출력→hook 적립 pending→approve 승격→무효 stdin 비간섭까지
// 2계층 적립 자동화가 한 줄로 이어지는지 검증한다. 환경 격리 — 임시
// FLINT_CONFIG_DIR·FLINT_CLAUDE_HOME을 서브프로세스 env에 주입해 실제
// 사용자 마커·플러그인 캐시 오염을 막는다(Review Focus: 마커 오염 방지).
// 실행: go test -tags e2e ./tests/e2e/ -run TestV07AutomationChain -count=1
package e2e

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"flint/lib/db"
)

// flintEnv — os.Environ에서 overrides 키를 제거한 뒤 append(뒤가 아닌
// 앞 우선 적용 방지 — getenv는 첫 일치를 반환할 수 있다).
func flintEnv(overrides ...string) []string {
	drop := map[string]bool{}
	for _, kv := range overrides {
		k, _, _ := strings.Cut(kv, "=")
		drop[k] = true
	}
	var env []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if !drop[k] {
			env = append(env, kv)
		}
	}
	return append(env, overrides...)
}

// runFlint — bin/flint 서브프로세스 실행(체인 공용). 통합 출력과 exit 코드.
// 프로세스 기동 자체 실패는 즉시 Fatalf(훅 실패와 구분).
func runFlint(t *testing.T, bin string, env []string, args []string, stdin string) (string, int) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = env
	cmd.Stdin = strings.NewReader(stdin)
	var out strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &out
	code := 0
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			t.Fatalf("flint %s 기동 오류: %v; out=%s", strings.Join(args, " "), err, out.String())
		}
	}
	return out.String(), code
}

// TestV07AutomationChain — Review Focus(순서 의존·환경 격리): 8단계 체인.
// state: report 계측·마커 생명주기·pending→active 승격. protocol: 훅 진입
// 커맨드의 비간섭(어떤 실패에도 exit 0·조용히).
func TestV07AutomationChain(t *testing.T) {
	// 신규 DB 대응 — mustDB 뷰 재생성에 앞서 테이블 확보(v06 패턴).
	repo, err := db.NewRepo(context.Background(), dsn())
	if err != nil {
		t.Skipf("Doltgres 미기동: %v", err)
	}
	t.Cleanup(repo.Close)
	if err := repo.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	pool := mustDB(t)
	cleanupTopics(t, pool, "e2e-v07-")
	t.Cleanup(func() { cleanupTopics(t, pool, "e2e-v07-") })
	// compact가 남긴 session 행(agent=claude) 정리 — summary 프리픽스로 식별.
	cleanupSessions := func() {
		pool.Exec(context.Background(),
			`DELETE FROM sessions WHERE summary LIKE $1`, "e2e-v07%")
	}
	cleanupSessions()
	t.Cleanup(cleanupSessions)

	// 환경 격리 — 마커·플러그인 캐시를 임시 디렉터리로 돌린다.
	cfgDir := t.TempDir()
	env := flintEnv(
		"FLINT_CONFIG_DIR="+cfgDir,
		"FLINT_CLAUDE_HOME="+t.TempDir(),
		"FLINT_DSN="+dsn(),
	)
	markerPath := filepath.Join(cfgDir, "pending-distill.json")
	bin := rebuildBin(t)

	// ① PostCompact 훅 JSON → flint compact — exit 0·stdout 빈(비간섭).
	summary := "e2e-v07 컴팩트 요약 프로브"
	out, code := runFlint(t, bin, env, []string{"compact"},
		`{"hook_event_name":"PostCompact","session_id":"e2e-v07-s1","compact_summary":"`+summary+`"}`)
	if code != 0 {
		t.Fatalf("① compact exit=%d, want 0; out=%s", code, out)
	}
	if got := strings.TrimSpace(out); got != "" {
		t.Fatalf("① compact stdout=%q, want 빈(조용히)", got)
	}

	// ② 마커 존재 — compact 적립 성공의 증거. session_id까지 정합.
	// report 계측(③)보다 먼저 확인해 마커 귀속 순서를 명확히 한다.
	if _, err := os.Stat(markerPath); err != nil {
		t.Fatalf("② 마커 미생성(%s): %v", markerPath, err)
	}
	mb, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatalf("② 마커 읽기: %v", err)
	}
	mm := decode(t, string(mb))
	if mm["session_id"] != "e2e-v07-s1" {
		t.Fatalf("② 마커 session_id=%v, want e2e-v07-s1; out=%s", mm["session_id"], mb)
	}

	// ③ report --days 1 — 오늘 write≥1·hook≥1(소스 hook 유입 실측).
	out, code = runFlint(t, bin, env, []string{"report", "--days", "1"}, "")
	if code != 0 {
		t.Fatalf("③ report exit=%d, want 0; out=%s", code, out)
	}
	rm := decode(t, out)
	days, _ := rm["days"].([]any)
	if len(days) != 1 {
		t.Fatalf("③ days %d행, want 1; out=%s", len(days), out)
	}
	today, _ := days[0].(map[string]any)
	w, _ := today["write"].(float64)
	h, _ := today["hook"].(float64)
	if w < 1 || h < 1 {
		t.Fatalf("③ 오늘 write=%v hook=%v, want 각 >=1; out=%s", today["write"], today["hook"], out)
	}

	// ④ Stop 훅 JSON → flint nudge — stdout에 additionalContext 넛지.
	stopJSON := `{"hook_event_name":"Stop","session_id":"e2e-v07-s1","stop_hook_active":false}`
	out, code = runFlint(t, bin, env, []string{"nudge"}, stopJSON)
	if code != 0 {
		t.Fatalf("④ nudge exit=%d, want 0; out=%s", code, out)
	}
	nm := decode(t, out)
	hs, _ := nm["hookSpecificOutput"].(map[string]any)
	if hs == nil {
		t.Fatalf("④ hookSpecificOutput 없음; out=%s", out)
	}
	if hs["hookEventName"] != "Stop" {
		t.Fatalf("④ hookEventName=%v, want Stop; out=%s", hs["hookEventName"], out)
	}
	if ac, _ := hs["additionalContext"].(string); !strings.Contains(ac, "flint nudge") {
		t.Fatalf("④ additionalContext가 짧은 증류 지시문 아님(%q); out=%s", ac, out)
	}

	// ⑤ nudge --clear — 마커 소비·무출력.
	out, code = runFlint(t, bin, env, []string{"nudge", "--clear"}, "")
	if code != 0 {
		t.Fatalf("⑤ nudge --clear exit=%d, want 0; out=%s", code, out)
	}
	if got := strings.TrimSpace(out); got != "" {
		t.Fatalf("⑤ --clear stdout=%q, want 빈", got)
	}
	if _, err := os.Stat(markerPath); !os.IsNotExist(err) {
		t.Fatalf("⑤ 마커 잔존(err=%v), want 소멸", err)
	}

	// ⑥ 재 nudge — 마커 부재 → 빈 출력(1회성 넛지 방어).
	out, code = runFlint(t, bin, env, []string{"nudge"}, stopJSON)
	if code != 0 {
		t.Fatalf("⑥ 재 nudge exit=%d, want 0; out=%s", code, out)
	}
	if got := strings.TrimSpace(out); got != "" {
		t.Fatalf("⑥ 재 nudge stdout=%q, want 빈(마커 소진)", got)
	}

	// ⑦ hook 적립 pending → approve → active(레이어 2 증류 종결 경로).
	topic := "e2e-v07-k"
	out, code = runFlint(t, bin, env, []string{"write", "knowledge", "--source", "hook"},
		`{"topic":"`+topic+`","summary":"v0.7 체인 증류 프로브","confidence":0.7}`)
	if code != 0 {
		t.Fatalf("⑦ write exit=%d, want 0; out=%s", code, out)
	}
	wm := decode(t, out)
	if wm["status"] != "committed" || wm["state"] != "pending" {
		t.Fatalf("⑦ write status=%v state=%v, want committed/pending; out=%s",
			wm["status"], wm["state"], out)
	}
	id := int(wm["id"].(float64))
	out, code = runFlint(t, bin, env, []string{"approve", "--id", strconv.Itoa(id)}, "")
	if code != 0 {
		t.Fatalf("⑦ approve exit=%d, want 0; out=%s", code, out)
	}
	out, code = runFlint(t, bin, env,
		[]string{"read", topic, "--json", "--fields", "id,status"}, "")
	if code != 0 {
		t.Fatalf("⑦ read exit=%d, want 0; out=%s", code, out)
	}
	if sm := decode(t, out); sm["status"] != "active" {
		t.Fatalf("⑦ 승격 후 read status=%v, want active; out=%s", sm["status"], out)
	}

	// ⑧ 무효 stdin compact — exit 0·무변화(비간섭의 마지노선).
	out, code = runFlint(t, bin, env, []string{"compact"}, "not json")
	if code != 0 {
		t.Fatalf("⑧ 무효 stdin compact exit=%d, want 0; out=%s", code, out)
	}
	if got := strings.TrimSpace(out); got != "" {
		t.Fatalf("⑧ 무효 stdin compact stdout=%q, want 빈", got)
	}
}
