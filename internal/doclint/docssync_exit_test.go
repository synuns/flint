package doclint

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func docssyncRecipe(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(docPath("../Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	i := strings.Index(s, "\ndocs-sync:")
	if i < 0 {
		t.Fatal("Makefile에 docs-sync 타깃 없음")
	}
	rest := s[i+len("\ndocs-sync:"):]
	var lines []string
	for _, ln := range strings.Split(rest, "\n") {
		if strings.HasPrefix(ln, "\t") {
			lines = append(lines, ln)
		} else if len(lines) > 0 {
			break
		}
	}
	return strings.Join(lines, "\n")
}

func TestMakefileDocsSyncPropagatesGoTestExit(t *testing.T) {
	r := docssyncRecipe(t)

	if !regexp.MustCompile(`\$\(\s*go test`).MatchString(r) {
		t.Error("go test가 명령 치환 $( ) 안에서 실행되지 않음 — 파이프 앞단 배치는 grep|tail에 exit을 삼킴")
	}

	if !strings.Contains(r, "$$?") || !regexp.MustCompile(`exit\s+\$\$`).MatchString(r) {
		t.Error("go test exit 캡처($$?) 후 exit 반환 누락 — 레시피 실패가 make exit으로 전파되지 않음")
	}

	if !strings.Contains(r, `^(=== RUN|--- FAIL|--- PASS)`) || !strings.Contains(r, "tail -50") {
		t.Error("RUN/FAIL/PASS 필터·tail -50 출력 형식이 유지되지 않음")
	}
}
