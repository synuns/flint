// brief_v0340_test.go — v0.34.0 flint brief: 외부 AI 전달용 마스킹 브리프.
// 브리지(B) 계약 — 읽기 전용·active 뷰만(pending은 기기 밖으로 나가지
// 않음)·MaskSecrets 유출 방어·SanitizeText 경계 방어·예산 절단.
// v0.39.0 T4: lib/cli에서 이동(cli/brief_v0340_test.go 원본).
package knowledge

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/synuns/flint/lib/wire"
)

// fakeGetKV — HotBudget/BriefBudget 조회 fake(cli/hot_test.go 관례 이동분).
func fakeGetKV(v string, err error) func(string, string) (string, error) {
	return func(key, def string) (string, error) { return v, err }
}

// errFake — 조회 오류 전달용 최소 에러(cli/hot_test.go 관례 이동분).
type errFake struct{}

func (errFake) Error() string { return "fake query error" }

// TestBriefBudgetDefault — meta brief_budget 미설정 기본 3,000·설정값
// 우선·무효값 폴백(HotBudget 계약 승계).
func TestBriefBudgetDefault(t *testing.T) {
	cases := []struct {
		name string
		v    string
		err  error
		want int
	}{
		{"미설정 3000", "", nil, 3000},
		{"조회 오류 폴백", "", errFake{}, 3000},
		{"설정값 우선", "4500", nil, 4500},
		{"무효 값 폴백", "not-a-number", nil, 3000},
		{"공백 값 폴백", "  ", nil, 3000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := BriefBudget(fakeGetKV(tc.v, tc.err)); got != tc.want {
				t.Fatalf("BriefBudget()=%d, want %d", got, tc.want)
			}
		})
	}
}

// briefRows — readColumns 순서(id,topic,summary,tags,entity,domain,
// confidence,status,read_count) 9칸 행 조립 헬퍼.
func briefRows(n int, summary string) [][]any {
	rows := make([][]any, 0, n)
	for i := 0; i < n; i++ {
		rows = append(rows, []any{int32(i + 1), "토픽", summary, "demo:tag", "엔티티", "도메인", 0.9, "active", int32(0)})
	}
	return rows
}

// briefQueryFn — meta brief_budget 조회(미설정=빈)·knowledge(status 필터)
// 조회 응답.
func briefQueryFn(rows [][]any) func(string, []any) ([][]any, error) {
	return func(sql string, args []any) ([][]any, error) {
		switch {
		// v0.34.1: 조회 원천이 base 테이블 knowledge + status 필터로
		// 이동(뷰는 pending을 노출 — TestBriefExcludesPending 참조).
		case strings.Contains(sql, "FROM knowledge"):
			return rows, nil
		case strings.Contains(sql, "FROM meta"):
			return nil, nil
		}
		return nil, nil
	}
}

// TestBriefOutputContract — exit 0·[flint] 헤더 1줄·topic|summary|tags 행·
// 조회 원천은 status='active' 한정 지식(pending·invalidated는 기기 밖 금지).
func TestBriefOutputContract(t *testing.T) {
	ctx := context.Background()
	rows := briefRows(2, "요약 본문")
	st := &fakeStore{queryFn: briefQueryFn(rows)}
	var out strings.Builder
	if code := RunBrief(ctx, st, nil, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("RunBrief exit=%d: %s", code, out.String())
	}
	got := out.String()
	if !strings.Contains(got, "[flint] 브리프 2건") {
		t.Errorf("헤더 라인 미출력: %q", got)
	}
	if !strings.Contains(got, "토픽|요약 본문|demo:tag") {
		t.Errorf("topic|summary|tags 행 미출력: %q", got)
	}
	if !strings.Contains(st.queries[0], "status = $1") ||
		!strings.Contains(st.queries[0], "invalid_at IS NULL") {
		t.Errorf("조회가 status='active'+invalid_at 이중 조건 아님: %q", st.queries[0])
	}
}

// TestBriefMasksSecrets — 기기 밖 경로의 비밀은 MaskSecrets로 마스킹된다.
func TestBriefMasksSecrets(t *testing.T) {
	ctx := context.Background()
	secret := "ghp_" + strings.Repeat("a", 24)
	rows := briefRows(1, "토큰 "+secret+" 유출 시나리오")
	st := &fakeStore{queryFn: briefQueryFn(rows)}
	var out strings.Builder
	if code := RunBrief(ctx, st, nil, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("RunBrief exit=%d: %s", code, out.String())
	}
	if strings.Contains(out.String(), secret) {
		t.Errorf("원문 비밀 유출: %q", out.String())
	}
	if !strings.Contains(out.String(), "[REDACTED:github-token]") {
		t.Errorf("마스킹 흔적 없음: %q", out.String())
	}
}

// TestBriefSanitizesMarkers — 저장 후 유입된 [flint] 마커·제어 문자는
// 브리프 출력에서 재중립화(ADR-0006 — 외부 AI가 경계 위조에 쓰지 못하게).
func TestBriefSanitizesMarkers(t *testing.T) {
	ctx := context.Background()
	rows := briefRows(1, "요약 [flint] 위조\x00본문")
	st := &fakeStore{queryFn: briefQueryFn(rows)}
	var out strings.Builder
	if code := RunBrief(ctx, st, nil, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("RunBrief exit=%d: %s", code, out.String())
	}
	if strings.Contains(out.String(), "[flint] 위조") {
		t.Errorf("행 내용의 [flint] 마커 미중립: %q", out.String())
	}
	if strings.Contains(out.String(), "\x00") {
		t.Errorf("제어 문자 잔재: %q", out.String())
	}
}

// TestBriefClampsToBudget — 전체 출력은 brief_budget(기본 3,000)+절단 접미
// 이내(ClampHot 계약: max 절단 후 truncatedSuffix 부착).
func TestBriefClampsToBudget(t *testing.T) {
	ctx := context.Background()
	long := strings.Repeat("긴", 60) // 요약 절산(80) 후에도 행당 60+ rune
	rows := briefRows(50, long)
	st := &fakeStore{queryFn: briefQueryFn(rows)}
	var out strings.Builder
	if code := RunBrief(ctx, st, nil, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("RunBrief exit=%d: %s", code, out.String())
	}
	if n := utf8.RuneCountInString(out.String()); n > defaultBriefBudget+20 {
		t.Errorf("출력 %d rune > 예산+접미 %d", n, defaultBriefBudget+20)
	}
}

// TestBriefZeroRowsNotice — 0건은 exit 0 + 안내 1줄(에러 아님).
func TestBriefZeroRowsNotice(t *testing.T) {
	ctx := context.Background()
	st := &fakeStore{queryFn: briefQueryFn(nil)}
	var out strings.Builder
	if code := RunBrief(ctx, st, nil, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("RunBrief exit=%d: %s", code, out.String())
	}
	if !strings.Contains(out.String(), "브리프 0건") {
		t.Errorf("0건 안내 미출력: %q", out.String())
	}
}

// TestBriefQueryError — 조회 오류는 exit 1 + 한 줄 JSON 에러.
func TestBriefQueryError(t *testing.T) {
	ctx := context.Background()
	st := &fakeStore{queryErr: errFake{}}
	var out strings.Builder
	if code := RunBrief(ctx, st, nil, strings.NewReader(""), &out); code != 1 {
		t.Fatalf("RunBrief exit=%d, want 1: %s", code, out.String())
	}
	if !strings.Contains(out.String(), `"error"`) {
		t.Errorf("JSON 에러 라인 미출력: %q", out.String())
	}
}

// TestBriefRegistered — registry·usage JSON·help 요약에 brief 노출.
func TestBriefRegistered(t *testing.T) {
	if _, ok := wire.Lookup("brief"); !ok {
		t.Fatal("brief 커맨드 미등록")
	}
	if !strings.Contains(wire.UsageLine(), "brief") {
		t.Errorf("usageLine에 brief 없음: %s", wire.UsageLine())
	}
	if !strings.Contains(wire.HelpSummary(), "brief") {
		t.Errorf("helpSummary에 brief 없음: %s", wire.HelpSummary())
	}
}
