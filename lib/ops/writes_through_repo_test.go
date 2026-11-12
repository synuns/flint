// writes_through_repo_test.go — v0.41.0 T3: 쓰기 가드. lib/{ops,knowledge}
// 비테스트 소스의 Query/queryOnce 호출부에 UPDATE|DELETE|INSERT 토큰이
// 있으면 FAIL — 쓰기 SQL 리터럴은 db.Repo 단일 경로(Invalidate·Supersede·
// PruneInvalidated·MetaDelete·UpdateSessionSummary)만 소유한다.
// 호출부 단위 판정이라 조회용 문자열·출력 hint의 쓰기 단어는 오염되지
// 않는다(AST 기반 — Fprintln 인자는 검사 대상 아님).
package ops

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// writeTokenRe — SQL 쓰기 동사(단어 경계 — "deleted"·"updated_at" 등
// 컬럼·별칭은 미매칭).
var writeTokenRe = regexp.MustCompile(`(?i)\b(UPDATE|DELETE|INSERT)\b`)

// writeGuardDirs — 가드 대상 패키지 디렉터리(lib/ops·lib/knowledge).
var writeGuardDirs = []string{".", "../knowledge"}

// TestWritesThroughRepoOnly — Review Focus: 가드 위반 0(전소스).
// Query/queryOnce 호출부의 문자열 리터럴(이어붙이기 조각 포함)에 쓰기
// 토큰이 있으면 위치와 함께 FAIL.
func TestWritesThroughRepoOnly(t *testing.T) {
	for _, dir := range writeGuardDirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("디렉터리 읽기 %s: %v", dir, err)
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			path := filepath.Join(dir, name)
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				t.Fatalf("파싱 %s: %v", path, err)
			}
			ast.Inspect(f, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				var fname string
				switch fn := call.Fun.(type) {
				case *ast.SelectorExpr:
					fname = fn.Sel.Name
				case *ast.Ident:
					fname = fn.Name
				}
				if fname != "Query" && fname != "QueryOnce" {
					return true
				}
				// 호출부 인자의 문자열 리터럴 전수 수집(이어붙이기 조각 포함).
				var lits []string
				ast.Inspect(call, func(m ast.Node) bool {
					if lit, ok := m.(*ast.BasicLit); ok && lit.Kind == token.STRING {
						lits = append(lits, lit.Value)
					}
					return true
				})
				if writeTokenRe.MatchString(strings.Join(lits, "\n")) {
					t.Errorf("%s: Query/queryOnce 호출부 쓰기 토큰 — db.Repo 단일 경로 위반",
						fset.Position(call.Pos()))
				}
				return true
			})
		}
	}
}
