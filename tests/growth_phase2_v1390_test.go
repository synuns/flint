package tests

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/synuns/flint/internal/testengine"
	"github.com/synuns/flint/lib/cli"
	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/knowledge"
)

const v1390Label = "s-v1390-e2e"

func TestRealDBV1390SessionChain(t *testing.T) {
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

	ctx := context.Background()
	pool, err := db.Connect(ctx, testengine.ResolveDSN())
	if err != nil {
		if os.Getenv("FLINT_REALDB_REQUIRED") == "1" {
			t.Fatalf("전용 테스트 엔진 접속 실패(FLINT_REALDB_REQUIRED=1): %v", err)
		}
		t.Skipf("doltgres unavailable: %v", err)
	}
	t.Cleanup(pool.Close)

	t.Setenv("FLINT_DSN", testengine.ResolveDSN())
	cfgDir := t.TempDir()
	t.Setenv("FLINT_CONFIG_DIR", cfgDir)

	repo, err := db.NewRepo(ctx, testengine.ResolveDSN())
	if err != nil {
		if os.Getenv("FLINT_REALDB_REQUIRED") == "1" {
			t.Fatalf("전용 테스트 엔진 스토어 실패(FLINT_REALDB_REQUIRED=1): %v", err)
		}
		t.Skipf("doltgres unavailable: %v", err)
	}
	t.Cleanup(repo.Close)

	st, err := cli.OpenServerStore()
	if err != nil {
		if os.Getenv("FLINT_REALDB_REQUIRED") == "1" {
			t.Fatalf("전용 테스트 엔진 스토어 실패(FLINT_REALDB_REQUIRED=1): %v", err)
		}
		t.Skipf("doltgres unavailable: %v", err)
	}
	t.Cleanup(st.Close)

	cleanup := func() {
		if _, err := pool.Exec(ctx,
			`DELETE FROM work_state WHERE task LIKE $1 OR session_label LIKE $2`,
			"v1390 chain%", "s-v1390-%"); err != nil {
			t.Fatalf("work_state 정리 실패: %v", err)
		}
		if _, err := pool.Exec(ctx,
			`DELETE FROM projects WHERE name LIKE $1`, "v1390 e2e demo %"); err != nil {
			t.Fatalf("projects 정리 실패: %v", err)
		}
	}
	cleanup()
	t.Cleanup(cleanup)

	for _, n := range []int{7, 9} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO projects (id, name) VALUES ($1, $2)`,
			n, fmt.Sprintf("v1390 e2e demo project %d", n)); err != nil {
			t.Fatalf("projects 픽스처 적재(id=%d): %v", n, err)
		}
	}

	if err := repo.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if err := repo.EnsureSchemaV20(ctx); err != nil {
		t.Fatalf("EnsureSchemaV20: %v", err)
	}
	if err := repo.EnsureSchemaV21(ctx); err != nil {
		t.Fatalf("EnsureSchemaV21: %v", err)
	}
	var version string
	if err := pool.QueryRow(ctx,
		`SELECT value FROM meta WHERE key = 'schema_version'`).Scan(&version); err != nil {
		t.Fatalf("schema_version 조회: %v", err)
	}
	if version != "21" {
		t.Fatalf("schema_version=%q, want 21", version)
	}

	writeLabelFile(t, cfgDir, v1390Label)

	var buf bytes.Buffer
	if code := knowledge.RunState(ctx, st, []string{
		"open", "--task", "v1390 chain p7", "--next", "verify p7",
		"--project", "7", "--session", v1390Label,
	}, strings.NewReader(""), &buf); code != 0 {
		t.Fatalf("state open p7 exit=%d; out=%s", code, buf.String())
	}
	buf.Reset()
	if code := knowledge.RunState(ctx, st, []string{
		"open", "--task", "v1390 chain p9", "--next", "verify p9",
		"--project", "9", "--session", "s-v1390-other",
	}, strings.NewReader(""), &buf); code != 0 {
		t.Fatalf("state open p9 exit=%d; out=%s", code, buf.String())
	}

	var projectID *int
	var label string
	if err := pool.QueryRow(ctx,
		`SELECT project_id, session_label FROM work_state WHERE task = $1`,
		"v1390 chain p7").Scan(&projectID, &label); err != nil {
		t.Fatalf("p7 행 조회: %v", err)
	}
	if projectID == nil || *projectID != 7 {
		t.Fatalf("p7 project_id=%v, want 7", projectID)
	}
	if label != v1390Label {
		t.Fatalf("p7 session_label=%q, want %q", label, v1390Label)
	}

	if got, err := repo.LatestProjectForSession(ctx, v1390Label); err != nil || got != 7 {
		t.Fatalf("LatestProjectForSession(%q) = (%d, %v), want (7, nil)", v1390Label, got, err)
	}
	if got, err := repo.LatestProjectForSession(ctx, ""); err != nil || got != 0 {
		t.Fatalf("LatestProjectForSession(\"\") = (%d, %v), want (0, nil)", got, err)
	}

	if _, err := pool.Exec(ctx,
		`UPDATE work_state SET updated_at = now() - interval '2 hours' WHERE task = $1`,
		"v1390 chain p7"); err != nil {
		t.Fatalf("p7 updated_at 조정: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE work_state SET updated_at = now() - interval '1 hour' WHERE task = $1`,
		"v1390 chain p9"); err != nil {
		t.Fatalf("p9 updated_at 조정: %v", err)
	}

	stateLines := func(out string) []string {
		var lines []string
		for _, l := range strings.Split(out, "\n") {
			if strings.HasPrefix(l, "state|") {
				lines = append(lines, l)
			}
		}
		return lines
	}
	indexOf := func(lines []string, task string) int {
		for i, l := range lines {
			if strings.Contains(l, task) {
				return i
			}
		}
		return -1
	}

	resolved := runHotCLI(t)
	resolvedLines := stateLines(resolved)
	i7 := indexOf(resolvedLines, "v1390 chain p7")
	i9 := indexOf(resolvedLines, "v1390 chain p9")
	if i7 == -1 || i9 == -1 {
		t.Fatalf("hot 스냅샷에 픽스처 행 없음: p7@%d p9@%d out=%s", i7, i9, resolved)
	}
	if i7 > i9 {
		t.Fatalf("프로젝트 7 우선 정렬 미적용: p7@%d 가 p9@%d 뒤; out=%s", i7, i9, resolved)
	}
	if i7 != 0 {
		t.Fatalf("프로젝트 7 행이 스냅샷 선두 아님: p7@%d; out=%s", i7, resolved)
	}

	writeLabelFile(t, cfgDir, "s-v1390-none")
	unresolvedLabeled := runHotCLI(t)
	removeLabelFiles(t, cfgDir)
	unresolvedNoLabel := runHotCLI(t)
	if unresolvedLabeled != unresolvedNoLabel {
		t.Fatalf("미해석 경로 바이트 불일치:\n labeled=%q\n nolabel=%q", unresolvedLabeled, unresolvedNoLabel)
	}

	unresolvedLines := stateLines(unresolvedNoLabel)
	u7 := indexOf(unresolvedLines, "v1390 chain p7")
	u9 := indexOf(unresolvedLines, "v1390 chain p9")
	if u7 == -1 || u9 == -1 {
		t.Fatalf("미해석 hot 스냅샷에 픽스처 행 없음: out=%s", unresolvedNoLabel)
	}
	if u9 > u7 {
		t.Fatalf("미해석 경로가 updated_at 순 위반: p9@%d 가 p7@%d 뒤; out=%s", u9, u7, unresolvedNoLabel)
	}
}

func runHotCLI(t *testing.T) string {
	t.Helper()
	var out bytes.Buffer
	if code := cli.Run([]string{"hot"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("hot exit=%d, want 0; out=%s", code, out.String())
	}
	return out.String()
}

func writeLabelFile(t *testing.T, dir, label string) {
	t.Helper()
	p := filepath.Join(dir, "session-v1390e2e.label")
	if err := os.WriteFile(p, []byte(label+"\n"), 0o600); err != nil {
		t.Fatalf("라벨 파일 기록: %v", err)
	}
}

func removeLabelFiles(t *testing.T, dir string) {
	t.Helper()
	ms, err := filepath.Glob(filepath.Join(dir, "session-*.label"))
	if err != nil {
		t.Fatalf("라벨 파일 glob: %v", err)
	}
	for _, m := range ms {
		if err := os.Remove(m); err != nil {
			t.Fatalf("라벨 파일 삭제: %v", err)
		}
	}
}
