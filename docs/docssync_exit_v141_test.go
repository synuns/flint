// docssync_exit_v141_test.go — v1.4.1 R23: make docs-sync 레시피가
// go test 실패 exit을 grep|tail 파이프가 삼키지 않고 전파하는지.
// 정적 단얫: go test 출력을 먼저 캡처해 exit($$?)을 보존하고 필터 출력
// 후 그 exit으로 빠져야 한다(release: docs-sync 게이트 공전 방지).
package docs

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// docssyncRecipe — Makefile에서 docs-sync 타깃의 레시피 본문(탭 들여쓰기
// 행)을 추출한다.
func docssyncRecipe(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("../Makefile")
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

	// go test가 파이프 앞단이 아닌 명령 치환($(...)) 안에서 실행돼
	// exit이 캡처돼야 한다.
	if !regexp.MustCompile(`\$\(\s*go test`).MatchString(r) {
		t.Error("go test가 명령 치환 $( ) 안에서 실행되지 않음 — 파이프 앞단 배치는 grep|tail에 exit을 삼킴")
	}
	// 캡처한 exit($$?)으로 명시적으로 exit해야 한다.
	if !strings.Contains(r, "$$?") || !regexp.MustCompile(`exit\s+\$\$`).MatchString(r) {
		t.Error("go test exit 캡처($$?) 후 exit 반환 누락 — 레시피 실패가 make exit으로 전파되지 않음")
	}
	// 필터 출력 형식(RUN/FAIL/PASS tail -50)은 유지돼야 한다.
	if !strings.Contains(r, `^(=== RUN|--- FAIL|--- PASS)`) || !strings.Contains(r, "tail -50") {
		t.Error("RUN/FAIL/PASS 필터·tail -50 출력 형식이 유지되지 않음")
	}
}
