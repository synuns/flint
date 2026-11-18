// diff_test.go — flint diff: 행 상세·요약 한 줄 JSON·무효 스펙 exit 3.
// wire.Store 계약(LogEntries 접두어·DiffRows·DiffCommits) canned 오버라이드
// 기반. SUPERLITE_REAL=1은 실구현 위임 seam.
package cli

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/synuns/flint/lib/wire"
)

// 실측(2026-10-24) 확정 해시 픽스처 — 접두어 정규화 응답용.
const (
	diffFromHash = "aaaa1111aaaa1111aaaa1111aaaa1111"
	diffToHash   = "bbbb2222bbbb2222bbbb2222bbbb2222"
)

// diffCliFake — diff 커맨드용 계약 canned: 접두어 정규화는 LogEntries,
// 행 상세는 DiffRows, 커밋 요약은 DiffCommits 오버라이드. 접두어 인수는
// logPrefixes에 기록(파라미터화 검증 — 질의 인자 전달 증명).
type diffCliFake struct {
	*fakeStore
	logPrefixes []string
}

func (f *diffCliFake) LogEntries(ctx context.Context, q wire.LogQuery) ([]wire.LogEntry, error) {
	if q.Prefix == "" {
		return nil, nil
	}
	f.logPrefixes = append(f.logPrefixes, q.Prefix)
	switch {
	case strings.HasPrefix(diffFromHash, q.Prefix):
		return []wire.LogEntry{{Hash: diffFromHash}}, nil
	case strings.HasPrefix(diffToHash, q.Prefix):
		return []wire.LogEntry{{Hash: diffToHash}}, nil
	}
	return nil, nil
}

func (f *diffCliFake) DiffRows(ctx context.Context, fromHash, toHash string) ([]wire.KnowledgeDiffRow, error) {
	// 실측 diff_type 분포: added 2·modified 1·removed 1(ID는 계약 string).
	return []wire.KnowledgeDiffRow{
		{DiffType: "added", ID: "7", Topic: "new-topic"},
		{DiffType: "added", ID: "8", Topic: "newer-topic"},
		{DiffType: "modified", ID: "3", Topic: "changed-topic"},
		{DiffType: "removed", ID: "5", Topic: "gone-topic"},
	}, nil
}

func (f *diffCliFake) DiffCommits(ctx context.Context, fromHash, toHash string) ([]wire.DiffCommitSummary, error) {
	return []wire.DiffCommitSummary{{Hash: diffToHash, Date: time.Now(), Message: "second write", DataChange: true}}, nil
}

// TestDiffAddedCounts — 기본 모드 한 줄 JSON 수치·행 상세.
func TestDiffAddedCounts(t *testing.T) {
	f := &diffCliFake{fakeStore: &fakeStore{}}
	withFakeStore(t, f)
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
	if first["diff_type"] != "added" || first["id"] != "7" {
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

// TestDiffSummaryShape — --summary는 행 없이 수치+커밋 요약만.
func TestDiffSummaryShape(t *testing.T) {
	f := &diffCliFake{fakeStore: &fakeStore{}}
	withFakeStore(t, f)
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

// TestDiffInvalidExit3 — 무효 as-of 스펙 exit 3(from·to 각각).
func TestDiffInvalidExit3(t *testing.T) {
	f := &diffCliFake{fakeStore: &fakeStore{}}
	withFakeStore(t, f)
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

// TestDiffPrefixParameterized — 해시 접두어는 LogQuery{Prefix} 값으로
// 전달된다(접두어가 질의 인자·계약 필드로 흐르는 캡처 증명 — SQL 접합 아님).
func TestDiffPrefixParameterized(t *testing.T) {
	f := &diffCliFake{fakeStore: &fakeStore{}}
	withFakeStore(t, f)
	out, code := mustRun(t, []string{"diff", "aaaa1", "bbbb2"}, "")
	if code != 0 {
		t.Fatalf("exit=%d out=%s", code, out)
	}
	if len(f.logPrefixes) != 2 || f.logPrefixes[0] != "aaaa1" || f.logPrefixes[1] != "bbbb2" {
		t.Fatalf("접두어 인자 기록: %v", f.logPrefixes)
	}
}

// TestDiffSeamReal — SUPERLITE_REAL=1 실구현 seam: write 2커밋(A·B) 후
// diff A B로 added ≥ 1·from/to 해시 일치. 해시는 커밋 메시지 세션 태그로
// 특정해 병행 커밋과 무관하게 결정적. 실DB 미기동 시 스킵. 실DB는
// FLINT_TEST_DSN 설정 시만(운영 기본 DSN 접속 금지).
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
	// 내 두 커밋을 세션 태그로 조회(최신순 → [B, A]). LogEntries 계약
	// (LatestN + MessageLike 필터 — 해시·date typed)으로 스펙은 ISO 시각을
	// 쓴다(asof.go의 16진 접두어 가정이 실측 Doltgres 해시와 불일치해
	// ISO 시각 스펙으로 지정 — 인시던트 기록 승계).
	ctx := context.Background()
	real, err := newRealStore(ctx, dsn)
	if err != nil {
		t.Skipf("real store unavailable: %v", err)
	}
	defer real.Close()
	entries, err := real.LogEntries(ctx, wire.LogQuery{
		LatestN: 2, MessageLike: `%[session:diff-seam]%`,
	})
	if err != nil || len(entries) != 2 {
		t.Fatalf("세션 커밋 조회 실패: entries=%d err=%v", len(entries), err)
	}
	latestHash, prevHash := entries[0].Hash, entries[1].Hash
	latestDate, prevDate := entries[0].Date, entries[1].Date
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
