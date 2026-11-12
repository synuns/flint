// import_fence_r2_test.go — v1.0.2 R2 회귀 2종.
// (1) CommonMark 닫는 펜스 길이 — 여는 펜스(````·~~~~)보다 짧은 펜스
// 라인(```·~~~)은 닫는 펜스가 아니라 코드 본문: 짧은 라인 뒤의 "## "는
// 헤딩이 아니어야 한다(펜스 내부 헤딩의 유사 지식 분할 방지).
// (2) import 경로의 '-' 접두 값은 이미 무효 인자 exit 3(v0.27.3 unknown
// flag) — 플래그 흡수 가드(ValidateFlagValue)와 대칭 계약임을 고정.
package knowledge

import (
	"context"
	"strings"
	"testing"
)

// TestFenceCloseRequiresOpenLength — 여는 펜스 이상 길이만 닫는 펜스.
// 4개 펜스를 3개 펜스로 닫으려는 문서는 닫힘으로 취급하면 그 뒤 "## 가짜
// 헤딩"이 섹션으로 분할된다(3 sections) — CommonMark 위반. 수정 후에는
// 펜스 내부 본문으로 흡수(2 sections).
func TestFenceCloseRequiresOpenLength(t *testing.T) {
	cases := []struct{ name, open, short, close string }{
		{"backtick 4>3", "````", "```", "````"},
		{"tilde 4>3", "~~~~", "~~~", "~~~~"},
	}
	for _, tc := range cases {
		body := "## A\n본문 A 충분합니다.\n\n" + tc.open + "\ncode\n" + tc.short +
			"\n## 가짜 헤딩\n" + tc.close + "\n\n## B\n본문 B 충분합니다.\n"
		secs := parseMarkdownSections(body)
		if len(secs) != 2 || secs[1].Heading != "B" {
			t.Fatalf("%s: 짧은 닫음 펜스 오판 — %d sections %+v", tc.name, len(secs), secs)
		}
		if !strings.Contains(secs[0].Text, "가짜 헤딩") {
			t.Errorf("%s: 펜스 내부 헤딩이 본문에 없음(분할 새어나감): %q", tc.name, secs[0].Text)
		}
	}
}

// TestFenceCloseEqualOrLongerStillCloses — 같은 길이·그 이상 닫는 펜스는
// 기존대로 닫는다(3 열고 3·4로 닫음 — 하한 규칙의 정상 방향 불변).
func TestFenceCloseEqualOrLongerStillCloses(t *testing.T) {
	for _, closing := range []string{"```", "````"} {
		body := "## 섹션\n본문 충분합니다.\n\n```\ncode\n## 펜스 내부\n" + closing + "\n\n## 다음\n본문 충분합니다.\n"
		secs := parseMarkdownSections(body)
		if len(secs) != 2 || secs[1].Heading != "다음" {
			t.Errorf("닫는 펜스 %q: 분할 붕괴 — %d sections %+v", closing, len(secs), secs)
		}
	}
}

// TestImportPathFlagLikeArgExits3 — import 경로 인자의 '-' 접두는 사용법
// 오류 exit 3(unknown flag) — backup --out·restore --file의
// ValidateFlagValue 가드와 같은 계약(흡수 오타 경로 무효 인자).
func TestImportPathFlagLikeArgExits3(t *testing.T) {
	f := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) { return nil, nil }}
	for _, args := range [][]string{
		{"-rules.md"},
		{"--commit", "-x"},
	} {
		var out strings.Builder
		code := RunImport(context.Background(), f, args, strings.NewReader(""), &out)
		if code != 3 {
			t.Errorf("%v: exit=%d, want 3 (출력 %s)", args, code, out.String())
		}
		if !strings.Contains(out.String(), "unknown flag") {
			t.Errorf("%v: unknown flag 에러 아님: %s", args, out.String())
		}
	}
}
