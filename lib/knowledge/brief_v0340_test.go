package knowledge

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/synuns/flint/lib/wire"
)

func fakeGetKV(v string, err error) func(string, string) (string, error) {
	return func(key, def string) (string, error) { return v, err }
}

type errFake struct{}

func (errFake) Error() string { return "fake query error" }

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

func briefRows(n int, summary string) [][]any {
	rows := make([][]any, 0, n)
	for i := 0; i < n; i++ {
		rows = append(rows, []any{int32(i + 1), "토픽", summary, "demo:tag", "엔티티", "도메인", 0.9, "active", int32(0)})
	}
	return rows
}

func briefQueryFn(rows [][]any) func(string, []any) ([][]any, error) {
	return func(sql string, args []any) ([][]any, error) {
		switch {

		case strings.Contains(sql, "FROM knowledge"):
			return rows, nil
		case strings.Contains(sql, "FROM meta"):
			return nil, nil
		}
		return nil, nil
	}
}

func TestBriefOutputContract(t *testing.T) {
	ctx := context.Background()
	rows := briefRows(2, "요약 본문")
	st := &fakeStore{queryFn: briefQueryFn(rows)}
	var out strings.Builder
	if code := RunBrief(ctx, st, nil, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("RunBrief exit=%d: %s", code, out.String())
	}
	got := out.String()
	if !strings.Contains(got, "[flint] brief 2") {
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

func TestBriefClampsToBudget(t *testing.T) {
	ctx := context.Background()
	long := strings.Repeat("긴", 60)
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

func TestBriefZeroRowsNotice(t *testing.T) {
	ctx := context.Background()
	st := &fakeStore{queryFn: briefQueryFn(nil)}
	var out strings.Builder
	if code := RunBrief(ctx, st, nil, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("RunBrief exit=%d: %s", code, out.String())
	}
	if !strings.Contains(out.String(), "brief 0") {
		t.Errorf("0건 안내 미출력: %q", out.String())
	}
}

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
