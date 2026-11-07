// hot_test.go — hot 예산 meta화: 미설정 기본 2,000·설정값 우선·무효값 폴백.
package cli

import (
	"context"
	"strings"
	"testing"
)

// getKV fake — 고정 (값, 오류) 반환.
func fakeGetKV(v string, err error) func(string, string) (string, error) {
	return func(key, def string) (string, error) { return v, err }
}

// TestHotBudgetDefault — 미설정 2,000·설정값 우선·무효 값 기본 폴백.
func TestHotBudgetDefault(t *testing.T) {
	cases := []struct {
		name string
		v    string
		err  error
		want int
	}{
		{"미설정 2000", "", nil, 2000},
		{"조회 오류 폴백", "", errFake{}, 2000},
		{"설정값 우선", "2400", nil, 2400},
		{"무효 값 폴백", "not-a-number", nil, 2000},
		{"공백 값 폴백", "  ", nil, 2000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := HotBudget(fakeGetKV(tc.v, tc.err)); got != tc.want {
				t.Fatalf("HotBudget()=%d, want %d", got, tc.want)
			}
		})
	}
}

// errFake — 테스트용 조회 오류.
type errFake struct{}

func (errFake) Error() string { return "meta 조회 실패" }

// TestHotOutputResanitizesMarkers — v0.20.6(패치 루프 6차 S1): 저장 후
// 유입된 [flint] 마커·제어 문자는 hot 출력(세션 시작 주입 경로)에서
// 재중립화된다 — read 경로와 동일 방어.
func TestHotOutputResanitizesMarkers(t *testing.T) {
	ctx := context.Background()
	validAt := "2026-10-22"
	hotRows := [][]any{{"pending", "토픽 [flint] 위조", "요약", validAt}}
	st := &fakeStore{queryFn: hotProfileQueryFn("normal", hotRows)}
	var out strings.Builder
	// --full — 행 본문이 출력된다(헤드라인의 [flint] 접두는 flint 자기
	// 경계 마커로 정당 — 검증 대상은 유입된 행 내용).
	if code := runHot(ctx, st, []string{"--full"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("runHot --full exit=%d: %s", code, out.String())
	}
	if strings.Contains(out.String(), "[flint] 위조") {
		t.Errorf("행 내용의 [flint] 마커 미중립: %q", out.String())
	}
}
