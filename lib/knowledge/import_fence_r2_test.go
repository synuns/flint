// import_fence_r2_test.go — two regressions.
// (1) CommonMark closing-fence length — a fence line (```·~~~) shorter
// than the opening fence (````·~~~~) is code content, not a closing
// fence: a "## " after the short line must not be a heading (preventing
// spurious knowledge splitting of in-fence headings).
// (2) A '-'-prefixed value on the import path is already the
// invalid-arg exit 3 (unknown flag) — pinned as the symmetric contract
// with the flag-swallowing guard (ValidateFlagValue).
package knowledge

import (
	"context"
	"strings"
	"testing"
)

// TestFenceCloseRequiresOpenLength — only a fence at least as long as the
// opener closes. If a document trying to close 4 fences with 3 were
// treated as closed, the "## fake heading" after it would split into a
// section (3 sections) — a CommonMark violation. After the fix it is
// absorbed as in-fence content (2 sections).
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

// TestFenceCloseEqualOrLongerStillCloses — equal-length·longer closing
// fences still close as before (opened with 3, closed with 3·4 — the
// normal direction of the lower-bound rule stays invariant).
func TestFenceCloseEqualOrLongerStillCloses(t *testing.T) {
	for _, closing := range []string{"```", "````"} {
		body := "## 섹션\n본문 충분합니다.\n\n```\ncode\n## 펜스 내부\n" + closing + "\n\n## 다음\n본문 충분합니다.\n"
		secs := parseMarkdownSections(body)
		if len(secs) != 2 || secs[1].Heading != "다음" {
			t.Errorf("닫는 펜스 %q: 분할 붕괴 — %d sections %+v", closing, len(secs), secs)
		}
	}
}

// TestImportPathFlagLikeArgExits3 — a '-' prefix on an import path
// argument is a usage error exit 3 (unknown flag) — the same contract as
// the ValidateFlagValue guard of backup --out·restore --file (a
// swallowed-typo path is an invalid argument).
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
