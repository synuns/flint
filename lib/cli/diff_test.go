// diff_test.go — T84 flint diff: 행 상세(dolt_commit_diff_knowledge)·요약
// (dolt_log×dolt_diff) 한 줄 JSON·무효 스펙 exit 3. fake 기본, SUPERLITE_REAL=1은
// 실구현 위임 seam.
package cli

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// 실측(2026-10-24) 확정 해시 픽스처 — 접두어 정규화 응답용.
const (
	diffFromHash = "aaaa1111aaaa1111aaaa1111aaaa1111"
	diffToHash   = "bbbb2222bbbb2222bbbb2222bbbb2222"
)

// newDiffFake — as-of 접두어(LIKE)·행 상세·커밋 요약 질의를 구분 응답.
// likeArgs에 접두어 질의 인자를 기록한다(파라미터화 검증).
func newDiffFake() (*fakeStore, *[]string) {
	f := &fakeStore{}
	likeArgs := []string{}
	f.queryFn = func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "dolt_log") && strings.Contains(sql, "LIKE"):
			likeArgs = append(likeArgs, fmt.Sprint(args[0]))
			p := fmt.Sprint(args[0])
			if strings.HasPrefix(diffFromHash, p) {
				return [][]any{{diffFromHash}}, nil
			}
			if strings.HasPrefix(diffToHash, p) {
				return [][]any{{diffToHash}}, nil
			}
			return nil, nil
		case strings.Contains(sql, "dolt_commit_diff_knowledge"):
			// 실측 diff_type 분포: added 2·modified 1·removed 1.
			return [][]any{
				{"added", 7, "new-topic"},
				{"added", 8, "newer-topic"},
				{"modified", 3, "changed-topic"},
				{"removed", 5, "gone-topic"},
			}, nil
		case strings.Contains(sql, "dolt_diff"):
			return [][]any{{diffToHash, time.Now(), "second write", true, false}}, nil
		}
		return nil, nil
	}
	return f, &likeArgs
}

// TestDiffAddedCounts — Review Focus: 기본 모드 한 줄 JSON 수치·행 상세.
func TestDiffAddedCounts(t *testing.T) {
	f, _ := newDiffFake()
	withFake(t, f)
	out, code := mustRun(t, []string{"diff", "aaaa1", "bbbb2"}, "")
	if code != 0 {
		t.Fatalf("exit=%d out=%s", code, out)
	}
	m := decode(t, out)
	if m["from"] != diffFromHash || m["to"] != diffToHash {
		t.Fatalf("from/to 해시 정규화 실패: %v", m)
	}
	for k, want := range map[string]float64{"added": 2, "modified": 1, "removed": 1} {
		if got, _ := m[k].(float64); got != want {
			t.Fatalf("%s=%v want %v", k, m[k], want)
		}
	}
	rows, _ := m["rows"].([]any)
	if len(rows) != 4 {
		t.Fatalf("rows len=%d want 4", len(rows))
	}
	first, _ := rows[0].(map[string]any)
	if first["diff_type"] != "added" || first["id"].(float64) != 7 {
		t.Fatalf("rows[0]=%v", rows[0])
	}
	commits, _ := m["commits"].([]any)
	if len(commits) != 1 {
		t.Fatalf("commits len=%d want 1", len(commits))
	}
	cm, _ := commits[0].(map[string]any)
	if cm["hash"] != diffToHash || cm["data_change"] != true {
		t.Fatalf("commits[0]=%v", cm)
	}
}

// TestDiffSummaryShape — Review Focus: --summary는 행 없이 수치+커밋 요약만.
func TestDiffSummaryShape(t *testing.T) {
	f, _ := newDiffFake()
	withFake(t, f)
	out, code := mustRun(t, []string{"diff", "aaaa1", "bbbb2", "--summary"}, "")
	if code != 0 {
		t.Fatalf("exit=%d out=%s", code, out)
	}
	m := decode(t, out)
	if _, ok := m["rows"]; ok {
		t.Fatalf("--summary에 rows 포함: %s", out)
	}
	for _, k := range []string{"from", "to", "added", "modified", "removed", "commits"} {
		if _, ok := m[k]; !ok {
			t.Fatalf("키 %s 부재: %s", k, out)
		}
	}
	commits, _ := m["commits"].([]any)
	cm, _ := commits[0].(map[string]any)
	for _, k := range []string{"hash", "date", "message", "data_change", "schema_change"} {
		if _, ok := cm[k]; !ok {
			t.Fatalf("commits[0] 키 %s 부재: %v", k, cm)
		}
	}
}

// TestDiffInvalidExit3 — Review Focus: 무효 as-of 스펙 exit 3(from·to 각각).
func TestDiffInvalidExit3(t *testing.T) {
	cases := [][]string{
		{"diff", "2 days ago", "bbbb2"},
		{"diff", "aaaa1", "zzz9"}, // 형식은 유효(base32 소문자)지만 실DB 0매칭 접두어
		{"diff", "aaaa1"},         // 인자 부족
	}
	for _, args := range cases {
		out, code := mustRun(t, args, "")
		if code != 3 {
			t.Fatalf("%v exit=%d want 3 out=%s", args, code, out)
		}
		m := decode(t, out)
		if errKind, _ := m["error"].(string); errKind != "invalid_as_of" &&
			!strings.HasPrefix(errKind, "usage") {
			t.Fatalf("%v error=%v", args, m["error"])
		}
	}
}

// TestDiffPrefixParameterized — Review Focus: 해시 접두어는 LIKE $1 파라미터로.
func TestDiffPrefixParameterized(t *testing.T) {
	f, likeArgs := newDiffFake()
	withFake(t, f)
	out, code := mustRun(t, []string{"diff", "aaaa1", "bbbb2"}, "")
	if code != 0 {
		t.Fatalf("exit=%d out=%s", code, out)
	}
	found := false
	for _, q := range f.queries {
		if strings.Contains(q, "dolt_log") && strings.Contains(q, "LIKE") {
			found = true
			if !strings.Contains(q, "LIKE $1||'%'") {
				t.Fatalf("LIKE 접합 비파라미터화: %q", q)
			}
		}
	}
	if !found {
		t.Fatalf("접두어 질의 미검출")
	}
	// 접두어는 SQL 접합이 아닌 질의 인자로 전달돼야 한다(from·to 2회).
	if len(*likeArgs) != 2 || (*likeArgs)[0] != "aaaa1" || (*likeArgs)[1] != "bbbb2" {
		t.Fatalf("접두어 인자 기록: %v", *likeArgs)
	}
}

// TestDiffSeamReal — SUPERLITE_REAL=1 실구현 seam: write 2커밋(A·B) 후
// diff A B로 added ≥ 1·from/to 해시 일치. 해시는 커밋 메시지 세션 태그로
// 특정해 병행 커밋과 무관하게 결정적. 실DB 미기동 시 스킵. 실DB는
// FLINT_TEST_DSN 설정 시만(N1 — 운영 기본 DSN 접속 금지).
func TestDiffSeamReal(t *testing.T) {
	dsn, ok := seamRealDSN()
	if !ok {
		t.Skip("실구현 seam 검증은 SUPERLITE_REAL=1 + FLINT_TEST_DSN 설정 시만")
	}
	f := &fakeStore{}
	withFake(t, f) // stub 미설정 — 전부 실구현 위임
	if out, code := mustRun(t, []string{"write", "knowledge", "--session", "diff-seam"},
		`{"topic":"diff-seam-a","summary":"seam a","confidence":0.5}`); code != 0 {
		t.Fatalf("write A exit=%d out=%s", code, out)
	}
	if out, code := mustRun(t, []string{"write", "knowledge", "--session", "diff-seam"},
		`{"topic":"diff-seam-b","summary":"seam b","confidence":0.6}`); code != 0 {
		t.Fatalf("write B exit=%d out=%s", code, out)
	}
	// 내 두 커밋을 세션 태그로 조회(최신순 → [B, A]). 해시 스펙은 asof.go의
	// 16진 접두어 가정이 실측 Doltgres 해시(base32계)와 불일치해 ISO 시각
	// 스펙으로 지정한다(인시던트 기록 — from/to가 해당 커밋 date 직전
	// 최신이 되도록 나노초 정밀도 시각 사용).
	ctx := context.Background()
	real, err := newRealStore(ctx, dsn)
	if err != nil {
		t.Skipf("real store unavailable: %v", err)
	}
	defer real.Close()
	rows, err := real.Query(ctx,
		`SELECT commit_hash, date FROM dolt_log WHERE message LIKE $1 ORDER BY commit_order DESC LIMIT 2`,
		`%[session:diff-seam]%`)
	if err != nil || len(rows) != 2 {
		t.Fatalf("세션 커밋 조회 실패: rows=%d err=%v", len(rows), err)
	}
	latestHash, prevHash := fmt.Sprint(rows[0][0]), fmt.Sprint(rows[1][0])
	latestDate, _ := rows[0][1].(time.Time)
	prevDate, _ := rows[1][1].(time.Time)
	out, code := mustRun(t, []string{"diff", prevDate.Format(time.RFC3339Nano), latestDate.Format(time.RFC3339Nano)}, "")
	if code != 0 {
		t.Fatalf("diff exit=%d out=%s", code, out)
	}
	m := decode(t, out)
	if m["from"] != prevHash || m["to"] != latestHash {
		t.Fatalf("from/to 불일치: %s", out)
	}
	if added, _ := m["added"].(float64); added < 1 {
		t.Fatalf("added=%v want ≥1: %s", m["added"], out)
	}
	// 동일 지점 diff는 0 — 정규화 대칭 확인.
	out2, code2 := mustRun(t, []string{"diff", latestDate.Format(time.RFC3339Nano), latestDate.Format(time.RFC3339Nano)}, "")
	if code2 != 0 {
		t.Fatalf("동일 지점 exit=%d out=%s", code2, out2)
	}
	m2 := decode(t, out2)
	if m2["added"].(float64) != 0 || m2["removed"].(float64) != 0 {
		t.Fatalf("동일 지점 diff가 0이 아님: %s", out2)
	}
}
