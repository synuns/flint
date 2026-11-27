package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/synuns/flint/internal/testengine"
	"github.com/synuns/flint/lib/cli"
	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/knowledge"
)

const gm1380Prefix = "e2e-gm1380-"

func gm1380Connect(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := db.Connect(ctx, testengine.ResolveDSN())
	if err != nil {
		if os.Getenv("FLINT_REALDB_REQUIRED") == "1" {
			t.Fatalf("전용 테스트 엔진 접속 실패(FLINT_REALDB_REQUIRED=1): %v", err)
		}
		t.Skipf("doltgres unavailable: %v", err)
	}
	return pool
}

func gm1380RunCLI(t *testing.T, args []string, stdin string) (string, int) {
	t.Helper()
	var out bytes.Buffer
	code := cli.Run(args, strings.NewReader(stdin), &out)
	return out.String(), code
}

func gm1380Decode(t *testing.T, out string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &m); err != nil {
		t.Fatalf("출력이 JSON 아님: %q: %v", out, err)
	}
	return m
}

func gm1380Write(t *testing.T, payload string) map[string]any {
	t.Helper()
	out, code := gm1380RunCLI(t, []string{"write", "knowledge"}, payload)
	if code != 0 {
		t.Fatalf("write exit=%d, want 0; out=%s", code, out)
	}
	m := gm1380Decode(t, out)
	if m["status"] != "committed" {
		t.Fatalf("write status=%v, want committed; out=%s", m["status"], out)
	}
	return m
}

func TestRealDBV1380RoundTrip(t *testing.T) {
	f, err := os.OpenFile(filepath.Join(os.TempDir(), "flint-drill.lock"),
		os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatalf("잠금 파일: %v", err)
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatalf("flock: %v", err)
	}
	defer func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }()

	pool := gm1380Connect(t)
	t.Cleanup(pool.Close)
	ctx := context.Background()

	t.Setenv("FLINT_DSN", testengine.ResolveDSN())
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())

	repo, err := db.NewRepo(ctx, testengine.ResolveDSN())
	if err != nil {
		if os.Getenv("FLINT_REALDB_REQUIRED") == "1" {
			t.Fatalf("전용 테스트 엔진 스토어 실패(FLINT_REALDB_REQUIRED=1): %v", err)
		}
		t.Skipf("doltgres unavailable: %v", err)
	}
	defer repo.Close()

	st, err := cli.OpenServerStore()
	if err != nil {
		if os.Getenv("FLINT_REALDB_REQUIRED") == "1" {
			t.Fatalf("전용 테스트 엔진 스토어 실패(FLINT_REALDB_REQUIRED=1): %v", err)
		}
		t.Skipf("doltgres unavailable: %v", err)
	}
	defer st.Close()

	cleanup := func() {
		if _, err := pool.Exec(ctx, `DELETE FROM knowledge WHERE topic LIKE $1`, gm1380Prefix+"%"); err != nil {
			t.Fatalf("knowledge 정리 실패: %v", err)
		}
		if _, err := pool.Exec(ctx, `DELETE FROM work_state WHERE task LIKE $1`, "gm1380 task%"); err != nil {
			t.Fatalf("work_state 정리 실패: %v", err)
		}
	}
	cleanup()
	t.Cleanup(cleanup)

	if err := repo.EnsureSchemaV20(ctx); err != nil {
		t.Fatalf("EnsureSchemaV20 1회: %v", err)
	}
	var version string
	if err := pool.QueryRow(ctx,
		`SELECT value FROM meta WHERE key = 'schema_version'`).Scan(&version); err != nil {
		t.Fatalf("schema_version 조회: %v", err)
	}
	if version != "20" {
		t.Fatalf("schema_version=%q, want 20", version)
	}
	if err := repo.EnsureSchemaV20(ctx); err != nil {
		t.Fatalf("EnsureSchemaV20 재호출(멱등): %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT value FROM meta WHERE key = 'schema_version'`).Scan(&version); err != nil {
		t.Fatalf("schema_version 재조회: %v", err)
	}
	if version != "20" {
		t.Fatalf("재호출 후 schema_version=%q, want 20", version)
	}

	dec := gm1380Write(t,
		`{"topic":"`+gm1380Prefix+`dec","summary":"growth model decision probe demo","confidence":0.9}`)
	if dec["state"] != "pending" {
		t.Fatalf("claim 없는 write state=%v, want pending(게이트 강제)", dec["state"])
	}
	decID := int(dec["id"].(float64))
	ev := gm1380Write(t,
		`{"topic":"`+gm1380Prefix+`ev","summary":"growth model event probe demo","claim":"event","confidence":0.9}`)
	evID := int(ev["id"].(float64))
	var evClaim string
	if err := pool.QueryRow(ctx,
		`SELECT claim FROM knowledge WHERE id = $1`, evID).Scan(&evClaim); err != nil {
		t.Fatalf("ev 행 조회: %v", err)
	}
	if evClaim != "event" {
		t.Fatalf("ev claim=%q, want event(적재 시점 축 확정)", evClaim)
	}

	out, code := gm1380RunCLI(t, []string{"read", gm1380Prefix}, "")
	if code != 0 {
		t.Fatalf("read exit=%d, want 0; out=%s", code, out)
	}
	lines := nonEmptyLines(out)
	if len(lines) != 1 || !strings.Contains(lines[0], gm1380Prefix+"dec") {
		t.Fatalf("seed 직후 read %d행, want 1(dec만·event 제외); out=%s", len(lines), out)
	}

	var buf bytes.Buffer
	if code := knowledge.RunState(ctx, st, []string{
		"open", "--task", "gm1380 task alpha", "--detail", "demo detail",
	}, strings.NewReader(""), &buf); code != 0 {
		t.Fatalf("state open alpha exit=%d; out=%s", code, buf.String())
	}
	alpha := gm1380Decode(t, buf.String())
	alphaID := int(alpha["id"].(float64))
	if alpha["status"] != "open" {
		t.Fatalf("state open status=%v, want open; out=%s", alpha["status"], buf.String())
	}
	buf.Reset()
	if code := knowledge.RunState(ctx, st, []string{
		"open", "--task", "gm1380 task beta", "--next", "verify beta",
	}, strings.NewReader(""), &buf); code != 0 {
		t.Fatalf("state open beta exit=%d; out=%s", code, buf.String())
	}

	if s, err := knowledge.ApproveWithClaim(ctx, st, decID, ""); err == nil || s != "" {
		t.Fatalf("ApproveWithClaim(claim 공백) = (%q, %v), want (\"\", exit3 에러)", s, err)
	}

	out, code = gm1380RunCLI(t, []string{"approve", "--id", strconv.Itoa(decID)}, "")
	if code != 3 {
		t.Fatalf("approve 무claim exit=%d, want 3; out=%s", code, out)
	}
	out, code = gm1380RunCLI(t, []string{
		"approve", "--id", strconv.Itoa(decID), "--claim", "decision"}, "")
	if code != 0 {
		t.Fatalf("approve --claim decision exit=%d, want 0; out=%s", code, out)
	}
	if am := gm1380Decode(t, out); am["status"] != "approved" || am["id"] != float64(decID) {
		t.Fatalf("approve --claim 출력=%v, want approved/%d", am, decID)
	}

	out, code = gm1380RunCLI(t, []string{"read", gm1380Prefix}, "")
	if code != 0 {
		t.Fatalf("승격 후 read exit=%d, want 0; out=%s", code, out)
	}
	lines = nonEmptyLines(out)
	if len(lines) != 1 {
		t.Fatalf("read 기본 결과 %d행, want 1(decision만·event 제외); out=%s", len(lines), out)
	}
	if !strings.Contains(lines[0], gm1380Prefix+"dec") {
		t.Fatalf("read 기본에 decision 행 없음: %s", lines[0])
	}

	buf.Reset()
	if code := knowledge.RunState(ctx, st, []string{
		"resolve", "--id", strconv.Itoa(alphaID), "--by", strconv.Itoa(decID),
	}, strings.NewReader(""), &buf); code != 0 {
		t.Fatalf("state resolve exit=%d; out=%s", code, buf.String())
	}

	buf.Reset()
	if code := knowledge.RunState(ctx, st, []string{"list"}, strings.NewReader(""), &buf); code != 0 {
		t.Fatalf("state list exit=%d; out=%s", code, buf.String())
	}
	if strings.Contains(buf.String(), "task alpha") {
		t.Fatalf("resolve된 alpha가 state list에 잔존: %s", buf.String())
	}
	if !strings.Contains(buf.String(), "task beta") {
		t.Fatalf("open beta가 state list에 없음: %s", buf.String())
	}

	out, code = gm1380RunCLI(t, []string{"hot"}, "")
	if code != 0 {
		t.Fatalf("hot exit=%d, want 0; out=%s", code, out)
	}
	if !strings.Contains(out, "state|gm1380 task beta|verify beta") {
		t.Fatalf("hot에 open 스냅샷 행 없음: %s", out)
	}
	if strings.Contains(out, "task alpha") {
		t.Fatalf("hot에 resolve된 alpha 주입됨: %s", out)
	}
}

func nonEmptyLines(out string) []string {
	var lines []string
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		if l != "" {
			lines = append(lines, l)
		}
	}
	return lines
}
