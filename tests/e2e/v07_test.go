//go:build e2e

package e2e

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/db"
)

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

func TestV07AutomationChain(t *testing.T) {

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

	cleanupSessions := func() {
		pool.Exec(context.Background(),
			`DELETE FROM sessions WHERE summary LIKE $1`, "e2e-v07%")
	}
	cleanupSessions()
	t.Cleanup(cleanupSessions)

	cfgDir := t.TempDir()
	env := flintEnv(
		"FLINT_CONFIG_DIR="+cfgDir,
		"FLINT_CLAUDE_HOME="+t.TempDir(),
		"FLINT_DSN="+dsn(),
	)
	markerPath := filepath.Join(cfgDir, "pending-distill.json")
	bin := rebuildBin(t)

	summary := "e2e-v07 컴팩트 요약 프로브"
	out, code := runFlint(t, bin, env, []string{"compact"},
		`{"hook_event_name":"PostCompact","session_id":"e2e-v07-s1","compact_summary":"`+summary+`"}`)
	if code != 0 {
		t.Fatalf("① compact exit=%d, want 0; out=%s", code, out)
	}
	if got := strings.TrimSpace(out); got != "" {
		t.Fatalf("① compact stdout=%q, want 빈(조용히)", got)
	}

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

	out, code = runFlint(t, bin, env, []string{"nudge"}, stopJSON)
	if code != 0 {
		t.Fatalf("⑥ 재 nudge exit=%d, want 0; out=%s", code, out)
	}
	if got := strings.TrimSpace(out); got != "" {
		t.Fatalf("⑥ 재 nudge stdout=%q, want 빈(마커 소진)", got)
	}

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
	out, code = runFlint(t, bin, env, []string{"approve", "--id", strconv.Itoa(id), "--claim", "lesson"}, "")
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

	out, code = runFlint(t, bin, env, []string{"compact"}, "not json")
	if code != 0 {
		t.Fatalf("⑧ 무효 stdin compact exit=%d, want 0; out=%s", code, out)
	}
	if got := strings.TrimSpace(out); got != "" {
		t.Fatalf("⑧ 무효 stdin compact stdout=%q, want 빈", got)
	}
}
