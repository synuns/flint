package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/synuns/flint/internal/testengine"
	"github.com/synuns/flint/lib/cli"
	"github.com/synuns/flint/lib/db"
)

const v1400Prefix = "e2e-v1400-"

func v1400Connect(t *testing.T) *pgxpool.Pool {
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

func v1400RunCLI(t *testing.T, args []string, stdin string) (string, int) {
	t.Helper()
	var out bytes.Buffer
	code := cli.Run(args, strings.NewReader(stdin), &out)
	return out.String(), code
}

func v1400Decode(t *testing.T, out string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &m); err != nil {
		t.Fatalf("출력이 JSON 아님: %q: %v", out, err)
	}
	return m
}

func v1400Items(m map[string]any) []map[string]any {
	raw, _ := m["items"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, e := range raw {
		if it, ok := e.(map[string]any); ok {
			out = append(out, it)
		}
	}
	return out
}

func v1400Find(items []map[string]any, id int) map[string]any {
	for _, it := range items {
		if n, ok := it["id"].(float64); ok && int(n) == id {
			return it
		}
	}
	return nil
}

func v1400Seed(t *testing.T, ctx context.Context, pool *pgxpool.Pool) map[string]int {
	t.Helper()
	ids := map[string]int{}
	for _, claim := range []string{"decision", "procedure", "lesson", "event"} {
		payload := fmt.Sprintf(
			`{"topic":%q,"summary":"v1400 e2e synthetic demo row %s","claim":%q,"confidence":0.9,"tags":["demo"]}`,
			v1400Prefix+claim, claim, claim)
		out, code := v1400RunCLI(t, []string{"write", "knowledge"}, payload)
		if code != 0 {
			t.Fatalf("write %s exit=%d, want 0; out=%s", claim, code, out)
		}
		m := v1400Decode(t, out)
		if m["status"] != "committed" {
			t.Fatalf("write %s status=%v, want committed; out=%s", claim, m["status"], out)
		}
		id := int(m["id"].(float64))
		var status string
		if err := pool.QueryRow(ctx,
			`SELECT status FROM knowledge WHERE id = $1`, id).Scan(&status); err != nil {
			t.Fatalf("seed %s 상태 조회: %v", claim, err)
		}
		if status != "active" {
			t.Fatalf("seed %s status=%q, want active(claim 적재)", claim, status)
		}
		if _, err := pool.Exec(ctx,
			`UPDATE knowledge SET ingested_at = now() - interval '400 days', valid_at = now() - interval '400 days' WHERE id = $1`, id); err != nil {
			t.Fatalf("seed %s 나이 조정: %v", claim, err)
		}
		ids[claim] = id
	}
	return ids
}

func v1400Tags(ctx context.Context, pool *pgxpool.Pool, ids map[string]int) (map[string]string, error) {
	out := map[string]string{}
	for claim, id := range ids {
		var tags string
		if err := pool.QueryRow(ctx,
			`SELECT COALESCE(tags, '') FROM knowledge WHERE id = $1`, id).Scan(&tags); err != nil {
			return nil, err
		}
		out[claim] = tags
	}
	return out, nil
}

func TestRealDBV1400ApplyVol(t *testing.T) {
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

	pool := v1400Connect(t)
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

	cleanup := func() {
		if _, err := pool.Exec(ctx,
			`DELETE FROM knowledge WHERE topic LIKE $1`, v1400Prefix+"%"); err != nil {
			t.Fatalf("knowledge 정리 실패: %v", err)
		}
	}
	cleanup()
	t.Cleanup(cleanup)

	seeded := v1400Seed(t, ctx, pool)

	wantVol := map[string]string{
		"decision":  "vol:immutable",
		"procedure": "vol:conditional",
		"lesson":    "vol:conditional",
		"event":     "vol:immutable",
	}

	out, code := v1400RunCLI(t, []string{"maintain", "--age-audit", "--limit", "50"}, "")
	if code != 0 {
		t.Fatalf("age-audit exit=%d, want 0; out=%s", code, out)
	}
	items := v1400Items(v1400Decode(t, out))
	for claim, id := range seeded {
		it := v1400Find(items, id)
		if it == nil {
			t.Fatalf("age-audit에 %s 행(id=%d) 없음", claim, id)
		}
		if got, _ := it["suggested_vol"].(string); got != wantVol[claim] {
			t.Fatalf("%s suggested_vol=%q, want %q (claim 기본값)", claim, got, wantVol[claim])
		}
		if got, _ := it["vol_tag"].(string); got != "" {
			t.Fatalf("%s vol_tag=%q, want 빈 값(미태깅)", claim, got)
		}
	}

	out, code = v1400RunCLI(t, []string{"maintain", "--age-audit", "--apply-vol", "--limit", "50"}, "")
	if code != 0 {
		t.Fatalf("apply-vol dry-run exit=%d, want 0; out=%s", code, out)
	}
	dry := v1400Decode(t, out)
	if dry["status"] != "apply-vol" || dry["applied"] != false {
		t.Fatalf("dry-run status/applied=%v/%v, want apply-vol/false; out=%s", dry["status"], dry["applied"], out)
	}
	for claim, id := range seeded {
		if it := v1400Find(v1400Items(dry), id); it == nil {
			t.Fatalf("dry-run 제안에 %s 행(id=%d) 없음", claim, id)
		}
	}
	tagsAfterDry, err := v1400Tags(ctx, pool, seeded)
	if err != nil {
		t.Fatalf("dry-run 후 태그 조회: %v", err)
	}
	for claim, tags := range tagsAfterDry {
		if strings.Contains(tags, ":vol:") {
			t.Fatalf("dry-run 후 %s 태그에 vol 병합됨: %q (--yes 없으면 무변경)", claim, tags)
		}
	}

	out, code = v1400RunCLI(t, []string{"maintain", "--age-audit", "--apply-vol", "--yes", "--limit", "50"}, "")
	if code != 0 {
		t.Fatalf("apply-vol --yes exit=%d, want 0; out=%s", code, out)
	}
	applied := v1400Decode(t, out)
	if applied["applied"] != true {
		t.Fatalf("apply-vol --yes applied=%v, want true; out=%s", applied["applied"], out)
	}
	for claim, id := range seeded {
		it := v1400Find(v1400Items(applied), id)
		if it == nil {
			t.Fatalf("적용 결과에 %s 행(id=%d) 없음", claim, id)
		}
		if got, _ := it["merged_tag"].(string); got != ":"+wantVol[claim]+":" {
			t.Fatalf("%s merged_tag=%q, want %q", claim, got, ":"+wantVol[claim]+":")
		}
	}

	tagsAfterApply, err := v1400Tags(ctx, pool, seeded)
	if err != nil {
		t.Fatalf("적용 후 태그 조회: %v", err)
	}
	for claim, tags := range tagsAfterApply {
		wantSuffix := ":" + wantVol[claim] + ":"
		if !strings.HasSuffix(tags, wantSuffix) {
			t.Fatalf("%s 태그=%q, want %q 접미 병합", claim, tags, wantSuffix)
		}
		if !strings.Contains(tags, ":demo:") {
			t.Fatalf("%s 태그=%q, want 기존 :demo: 보존", claim, tags)
		}
	}

	out, code = v1400RunCLI(t, []string{"maintain", "--age-audit", "--limit", "50"}, "")
	if code != 0 {
		t.Fatalf("회귀 age-audit exit=%d, want 0; out=%s", code, out)
	}
	items = v1400Items(v1400Decode(t, out))
	for claim, id := range seeded {
		it := v1400Find(items, id)
		switch wantVol[claim] {
		case "vol:immutable":
			if it != nil {
				t.Fatalf("회귀: %s 행(id=%d)이 여전히 age-audit에 존재 — 감쇠 판정은 승인 적용된 태그로만 변화해야 함", claim, id)
			}
		case "vol:conditional":
			if it == nil {
				t.Fatalf("회귀: %s 행(id=%d)이 age-audit에서 사라짐 — conditional은 면제 아님", claim, id)
			}
			if got, _ := it["vol_tag"].(string); got != "vol:conditional" {
				t.Fatalf("회귀: %s vol_tag=%q, want vol:conditional", claim, got)
			}
			if got, _ := it["suggested_vol"].(string); got != "" {
				t.Fatalf("회귀: %s suggested_vol=%q, want 빈 값(기존 태그 우선)", claim, got)
			}
		}
	}
}
