// writes_through_repo_test.go — v0.41.0 T3: 쓰기 가드. lib/{ops,knowledge}
// 비테스트 소스의 Query/queryOnce 호출부에 UPDATE|DELETE|INSERT 토큰이
// 있으면 FAIL — 쓰기 SQL 리터럴은 db.Repo 단일 경로(Invalidate·Supersede·
// PruneInvalidated·MetaDelete·UpdateSessionSummary)만 소유한다.
// 호출부 단위 판정이라 조회용 문자열·출력 hint의 쓰기 단어는 오염되지
// 않는다(AST 기반 — Fprintln 인자는 검사 대상 아님).
//
// v0.41.0 리뷰 보강(우회 차단 2종 — 실증 후 폐쇄):
//   - 별칭 경유(q := wire.QueryOnce; q(..., "UPDATE ...")) — Query/QueryOnce를
//     변수에 담은 별칭까지 추적해 동일하게 판정한다.
//   - 변수 조립(sql := "DELETE ..."; wire.QueryOnce(..., sql)) — 쓰기 동사로
//     시작하는 SQL 문자열 리터럴은 파일 내 위치 불문 금지한다(호출부 인자에
//     없어도 소유 자체가 위반 — 헬퍼 인자 경유도 같은 리터럴로 잡힌다).
package ops

import (
	"fmt"
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

// writeSQLStartRe — 쓰기 문 SQL 리터럴 본문(quote 제거 후 판정 — 변수·
// 헬퍼 조립 경유의 원천 리터럴 검출). SQL 형태 한정: "update-rules:"류
// 안내문·"update" 체크명 같은 비SQL 문자열은 미매칭.
var writeSQLStartRe = regexp.MustCompile(`(?is)^\s*(UPDATE\s+\S+|DELETE\s+FROM\b|INSERT\s+INTO\b)`)

// writeGuardDirs — 가드 대상 패키지 디렉터리(lib/ops·lib/knowledge).
var writeGuardDirs = []string{".", "../knowledge"}

// unquoteLiteral — BasicLit.Value에서 quote 문자(`"`, backtick)를 양끝
// 제거한 본문.
func unquoteLiteral(v string) string {
	if len(v) >= 2 && (v[0] == '`' || v[0] == '"') && v[len(v)-1] == v[0] {
		return v[1 : len(v)-1]
	}
	return v
}

// queryAliasNames — 파일 내 Query/QueryOnce 별칭 변수명 수집(`:=`/`=`,
// var 선언 전부 — q := wire.QueryOnce·st.QueryOnce 등).
func queryAliasNames(f *ast.File) map[string]bool {
	aliases := map[string]bool{}
	isQueryRef := func(x ast.Expr) bool {
		switch e := x.(type) {
		case *ast.SelectorExpr:
			return e.Sel != nil && (e.Sel.Name == "Query" || e.Sel.Name == "QueryOnce")
		case *ast.Ident:
			return e.Name == "Query" || e.Name == "QueryOnce"
		}
		return false
	}
	recordLHS := func(lhs []ast.Expr, rhs ast.Expr) {
		if !isQueryRef(rhs) {
			return
		}
		for _, l := range lhs {
			if id, ok := l.(*ast.Ident); ok && id.Name != "_" {
				aliases[id.Name] = true
			}
		}
	}
	ast.Inspect(f, func(n ast.Node) bool {
		switch st := n.(type) {
		case *ast.AssignStmt:
			recordLHS(st.Lhs, st.Rhs[0])
		case *ast.GenDecl:
			if st.Tok != token.VAR {
				return true
			}
			for _, spec := range st.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok || len(vs.Values) == 0 {
					continue
				}
				var lhs []ast.Expr
				for _, name := range vs.Names {
					lhs = append(lhs, name)
				}
				recordLHS(lhs, vs.Values[0])
			}
		}
		return true
	})
	return aliases
}

// TestWritesThroughRepoOnly — Review Focus: 가드 위반 0(전소스).
// Query/queryOnce(별칭 포함) 호출부의 문자열 리터럴(이어붙이기 조각
// 포함)에 쓰기 토큰이 있으면 위치와 함께 FAIL. 더불어 쓰기 동사로 시작하는
// SQL 리터럴은 호출부 밖(변수·헬퍼 인자)에 있어도 FAIL.
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
			for _, v := range writeGuardViolations(fset, f) {
				t.Errorf("%s: %s", path, v)
			}
		}
	}
}

// writeGuardViolations — 파싱된 파일 1건의 가드 위반 목록(위치 포함
// 문자열). TestWritesThroughRepoOnly 본체와 우회 회귀 테스트가 같은
// 판정 로직을 공유한다.
func writeGuardViolations(fset *token.FileSet, f *ast.File) []string {
	var violations []string
	aliases := queryAliasNames(f)
	// 우회 원천 차단: 쓰기 SQL 리터럴은 파일 내 위치 불문 금지.
	ast.Inspect(f, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		if writeSQLStartRe.MatchString(unquoteLiteral(lit.Value)) {
			violations = append(violations, fmt.Sprintf(
				"%s: 쓰기 SQL 리터럴(호출부 밖 소유 포함) — db.Repo 단일 경로 위반",
				fset.Position(lit.Pos())))
		}
		return true
	})
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
		if fname != "Query" && fname != "QueryOnce" && !aliases[fname] {
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
			violations = append(violations, fmt.Sprintf(
				"%s: Query/queryOnce(별칭 포함) 호출부 쓰기 토큰 — db.Repo 단일 경로 위반",
				fset.Position(call.Pos())))
		}
		return true
	})
	return violations
}

// TestWriteGuardBlocksBypasses — v0.41.0 리뷰 회귀: 별칭 경유
// (q := wire.QueryOnce)·변수 조립(sql := "DELETE ...") 우회가 가드에
// 잡히는지 합성 소스로 단언(실증된 구멍 2종의 폐쇄 영구화).
func TestWriteGuardBlocksBypasses(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{
			name: "별칭 경유 UPDATE",
			src: `package ops
func f() { q := wire.QueryOnce; q(ctx, st, "UPDATE knowledge SET status='invalidated' WHERE id=1", nil) }`,
		},
		{
			name: "변수 조립 DELETE",
			src: `package ops
func f() string { sql := "DELETE FROM meta WHERE key='demo'"; return sql }`,
		},
		{
			name: "var 선언 별칭 INSERT",
			src: `package ops
var q = wire.QueryOnce
func f() { q(ctx, st, "INSERT INTO meta VALUES ('k','v')", nil) }`,
		},
		{
			name: "비SQL 문자열 무관(오탐 방어)",
			src:  `package ops` + "\n" + `const hint = "update-rules: 소스 미탐지"`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, "synthetic.go", tc.src, 0)
			if err != nil {
				t.Fatalf("파싱: %v", err)
			}
			got := writeGuardViolations(fset, f)
			if tc.name == "비SQL 문자열 무관(오탐 방어)" {
				if len(got) != 0 {
					t.Fatalf("오탐: %v", got)
				}
				return
			}
			if len(got) == 0 {
				t.Fatalf("우회 미검출 — 가드 구멍")
			}
		})
	}
}
