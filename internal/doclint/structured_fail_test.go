package doclint

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
)

type structuredFailFinding struct {
	Check   string
	Exit    int
	File    string
	Line    int
	Fix     string
	Summary []string
}

const structuredSummaryMaxLines = 5

func formatStructuredFailure(f structuredFailFinding) string {
	lines := []string{
		fmt.Sprintf("검사 실패 %s — exit %d (자가수정 관례)", f.Check, f.Exit),
		fmt.Sprintf("파일:행 %s:%d", f.File, f.Line),
		fmt.Sprintf("수정처 %s", f.Fix),
	}
	summary := f.Summary
	if len(summary) > structuredSummaryMaxLines {
		summary = summary[:structuredSummaryMaxLines]
	}
	return strings.Join(append(lines, summary...), "\n")
}

var structuredFileLineRe = regexp.MustCompile(`^파일:행 \S+:\d+$`)

func validateStructuredFailure(out string) error {
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) < 4 {
		return fmt.Errorf("구조 부족(%d줄) — 헤더·파일:행·수정처·요약 최소 구성", len(lines))
	}
	if !strings.Contains(lines[0], "exit 3") {
		return fmt.Errorf("헤더에 exit 3 자가수정 관례 없음: %q", lines[0])
	}
	if !structuredFileLineRe.MatchString(lines[1]) {
		return fmt.Errorf("2행이 파일:행 아님: %q", lines[1])
	}
	fix := strings.TrimSpace(strings.TrimPrefix(lines[2], "수정처"))
	if !strings.HasPrefix(lines[2], "수정처 ") || fix == "" {
		return fmt.Errorf("3행에 수정처 없음: %q", lines[2])
	}
	summary := lines[3:]
	if len(summary) > structuredSummaryMaxLines {
		return fmt.Errorf("요약 %d줄 — %d줄 초과", len(summary), structuredSummaryMaxLines)
	}
	for i, s := range summary {
		if strings.TrimSpace(s) == "" {
			return fmt.Errorf("요약 %d행 빈 줄", i+1)
		}
	}
	return nil
}

func TestStructuredFailureOutput(t *testing.T) {
	f := structuredFailFinding{
		Check: "TestSurfaceContractDepth",
		Exit:  3,
		File:  "docs/README.md",
		Line:  262,
		Fix:   "lib/cli/seed.go (bare usage 분기)",
		Summary: []string{
			"flint seed → exit 3 (want 0 대비 — 합성 데모 시나리오)",
			`출력: {"error":"usage","message":"seed: --demo required"}`,
			"커맨드 표 seed 행 — 필수 플래그 --demo 부재",
		},
	}
	out := formatStructuredFailure(f)
	if err := validateStructuredFailure(out); err != nil {
		t.Errorf("구조화 출력 계약 미준수: %v — 출력\n%s", err, out)
	}
	if !strings.Contains(out, "README.md:262") || !strings.Contains(out, "수정처 lib/cli/seed.go") {
		t.Errorf("파일:행·수정처 누락 — 출력\n%s", out)
	}

	long := structuredFailFinding{
		Check: "demo", Exit: 3, File: "docs/README.md", Line: 1, Fix: "fix",
		Summary: []string{"1", "2", "3", "4", "5", "6", "7"},
	}
	if err := validateStructuredFailure(formatStructuredFailure(long)); err != nil {
		t.Errorf("요약 5줄 절단 후에도 계약 미준수: %v", err)
	}

	noncompliant := []struct {
		name string
		out  string
	}{
		{"파일:행 없음(구조 미준수 재현)", "검사 실패 demo — exit 3\n수정처 somewhere\n요약 한 줄"},
		{"수정처 없음", "검사 실패 demo — exit 3\n파일:행 docs/README.md:1\n요약 한 줄"},
		{"요약 6줄(5줄 초과)", "검사 실패 demo — exit 3\n파일:행 docs/README.md:1\n수정처 x\n1\n2\n3\n4\n5\n6"},
		{"exit 3 관례 불일치", "검사 실패 demo — exit 1\n파일:행 docs/README.md:1\n수정처 x\n요약"},
	}
	for _, b := range noncompliant {
		if err := validateStructuredFailure(b.out); err == nil {
			t.Errorf("%s: 미준수 출력을 구조 검증이 통과시킴 — %q", b.name, b.out)
		}
	}
}
