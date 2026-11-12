// writes_through_repo_test.go — v0.41.0 T3: 쓰기 가드. lib/{ops,knowledge}
// 비테스트 소스의 Query/queryOnce 호출부에 UPDATE|DELETE|INSERT 토큰이
// 있으면 FAIL — 이 가드의 스코프(lib/{ops,knowledge})에서 쓰기 SQL 리터럴은
// db.Repo 경로(Invalidate·Supersede·PruneInvalidated·MetaDelete·
// UpdateSessionSummary)만 소유한다. v1.0.2 R2 정정: "db.Repo 단일 경로"는
// 이 스코프 한정 주장이다 — 스코프 밖(lib/wire/meta.go의 meta INSERT 등)은
// 이 가드가 보지 않는 별도 소유다.
//
// v0.41.0 리뷰 보강(우회 차단 2종 — 실증 후 폐쇄):
//   - 별칭 경유(q := wire.QueryOnce; q(..., "UPDATE ...")) — Query/QueryOnce를
//     변수에 담은 별칭까지 추적해 동일하게 판정한다.
//   - 변수 조립(sql := "DELETE ..."; wire.QueryOnce(..., sql)) — 쓰기 동사로
//     시작하는 SQL 문자열 리터럴은 파일 내 위치 불문 금지한다.
//
// v1.0.1 R1-D 보강(실증 우회 2종 추가 폐쇄):
//   - 조각 조립·Sprintf 인자("SELECT 1; "+"DELETE"·fmt.Sprintf 포함) —
//     쓰기 동사 토큰은 호출부·리터럴 선두 같은 위치 조건 없이 문자열
//     리터럴 본문 어디에나 있으면 위반이다(위치·경로 보고).
//   - 동사 분할 조립("UP"+"DATE …") — 이어붙이기(+ ) 피연산자 리터럴을
//     순서대로 합친 결과에서만 성립하는 쓰기 동사도 잡는다.
//   - 순회 사각 — 비재귀 os.ReadDir 대신 WalkDir 재귀(하위 디렉터리 전부,
//     Go 관례 testdata 제외).
//
// v1.0.2 R2 보강(실증 우회 2종 추가 폐쇄):
//   - 면제×조립 교차("update"+" knowledge …") — 화이트리스트 면제는
//     단독 리터럴 판정에만 적용하고, 조립(+)·Sprintf 결합 판정에는 면제
//     피연산자 본문도 참여한다. 조립·결합 결과 전체가 화이트리스트 본문과
//     정확 일치하면 면제다(정확 일치 원칙의 결합 수준 확장).
//   - Sprintf 동사 분할(Sprintf("DEL%s…","ETE")) — 형식 리터럴의 %s·%v
//     동사에 문자열 리터럴 인자를 대입한 결합 결과에서만 성립하는 쓰기
//     동사도 잡는다(sprintfCombined).
//
// v1.0.3 R3 보강(실증 우회 3종 추가 폐쇄 — 재프로브로 우회 확인 후):
//   - 중첩 Sprintf(Sprintf("DEL%s…", Sprintf("%s","ETE"))) — 비리터럴
//     인자가 Sprintf 결과면 1단계 재귀 결합해 판정한다(stringOperand).
//   - 상수 접기(const p = "DEL"; … p+q) — 파일 패키지 블록 const 문자열
//     테이블을 접어 Ident를 값으로 결합 판정(②③④)에 참여시킨다
//     (constStringTable).
//   - strings.Join 결합(strings.Join([]string{"DEL","ETE…"}, "")) — 슬라이스
//     리터럴 요소를 구분자와 합친 결과에서만 성립하는 쓰기 동사도 잡는다(④).
//
// v1.0.4 R5 보강(실증 우회 3종 추가 폐쇄 — 재프로브로 우회 확인 후):
//   - fmt.Sprint/Sprintln 결합(fmt.Sprint("DEL","ETE …")) — ③을 Sprintf
//     형식 대입에 한정하지 않고 Sprint(무구분자)·Sprintln(공백 구분자)
//     피연산자 결합까지 공통 판정으로 확장한다(fmtCombined).
//   - var 슬라이스 접기(var parts = []string{"DEL","ETE …"} + Join) —
//     패키지 블록 var []string 리터럴 테이블을 접어 Join 첫 인자 Ident를
//     해석한다(varStringSliceTable).
//   - strings.Builder 결합(b.WriteString("DEL"); b.WriteString("ETE …")) —
//     동일 Ident 수신자에 연속한 WriteString 인자를 이어붙인 결과에서만
//     성립하는 쓰기 동사도 잡는다(⑤).
//
// 오탐(안내문·검사명의 비SQL "update" 단어)은 writeLiteralAllow 정확
// 일치 화이트리스트로만 면제한다 — 신규 면제는 이 표에 대한 명시적
// 선언이어야 한다.
package ops

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// writeTokenRe — SQL 쓰기 동사(단어 경계 — "deleted"·"updated_at"·
// "would_delete" 등 컬럼·별칭은 미매칭).
var writeTokenRe = regexp.MustCompile(`(?i)\b(UPDATE|DELETE|INSERT)\b`)

// writeLiteralAllow — 오탐 화이트리스트: 쓰기 동사 단어를 담았으나 SQL이
// 아닌 안내문·검사명 리터럴의 본문 정확 일치. v1.0.1 전면화(토큰이면
// 위치 불문 위반) 이후 정상 소스가 통과하려면 이 표 선언이 유일한 면제
// 경로다 — 본문 수정 시 이 표도 함께 갱신한다(자동 면제 없음).
var writeLiteralAllow = map[string]bool{
	// doctor 검사명·처방 문구(사람 대향 안내, 비SQL).
	"update":            true,
	"fix: flint update": true,
	"fix: flint setup --update-rules — 스킬 싱크": true,
	// rules_sync 오류 안내문.
	"update-rules: 소스 flint-plugin/skills/flint 미탐지 — --src 플래그로 지정": true,
	"update-rules: FLINT_CLAUDE_HOME 또는 HOME을 설정해 싱크 대상 경로를 지정":      true,
	"update-rules 싱크 실패: %w": true,
	// maintain unknown-키 힌트: 사람이 수동 실행할 문구 안내(실행 아님).
	`{"hint":"unknown 키는 사용 중단 후보 — 확인 후 수동 정리: DELETE FROM meta WHERE key = '...'"}`: true,
}

// writeGuardDirs — 가드 대상 패키지 루트(lib/ops·lib/knowledge). 하위
// 디렉터리까지 재귀 순회한다(R1-D: 비재귀 ReadDir의 하위 디렉터리 사각
// 폐쇄).
var writeGuardDirs = []string{".", "../knowledge"}

// unquoteLiteral — BasicLit.Value에서 quote 문자(`"`, backtick)를 양끝
// 제거한 본문.
func unquoteLiteral(v string) string {
	if len(v) >= 2 && (v[0] == '`' || v[0] == '"') && v[len(v)-1] == v[0] {
		return v[1 : len(v)-1]
	}
	return v
}

// writeGuardScan — dir 루트를 재귀(WalkDir) 순회해 비테스트 .go 파일
// 전부의 가드 위반을 모은다(위반 문자열은 "path:line:col: 사유" 형태로
// 스스로 경로를 밝힌다).
func writeGuardScan(dir string) []string {
	var out []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "testdata" {
				return fs.SkipDir
			}
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return fmt.Errorf("파싱 %s: %w", path, err)
		}
		out = append(out, writeGuardViolations(fset, f)...)
		return nil
	})
	if err != nil {
		return append(out, fmt.Sprintf("순회 %s: %v", dir, err))
	}
	return out
}

// TestWritesThroughRepoOnly — Review Focus: 가드 위반 0(전소스·하위
// 디렉터리 포함). 쓰기 동사 토큰이 문자열 리터럴(조각·Sprintf 인자 포함,
// 위치 불문) 또는 이어붙이기 조립 결과에 등장하면 위치와 함께 FAIL.
func TestWritesThroughRepoOnly(t *testing.T) {
	for _, dir := range writeGuardDirs {
		for _, v := range writeGuardScan(dir) {
			t.Error(v)
		}
	}
}

// writeGuardViolations — 파싱된 파일 1건의 가드 위반 목록(위치 포함
// 문자열). TestWritesThroughRepoOnly 본체와 우회 회귀 테스트가 같은
// 판정 로직을 공유한다.
func writeGuardViolations(fset *token.FileSet, f *ast.File) []string {
	var violations []string
	report := func(pos token.Pos, why string) {
		violations = append(violations, fmt.Sprintf("%s: %s", fset.Position(pos), why))
	}
	// ① 문자열 리터럴 전수 — 본문 어디에든 쓰기 동사 단어 토큰이면 위반
	//    (호출부 인자·변수 조립·Sprintf 인자 구분 없음). 화이트리스트
	//    정확 일치 본문만 면제.
	flagged := map[token.Pos]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		body := unquoteLiteral(lit.Value)
		if writeLiteralAllow[body] {
			return true
		}
		if writeTokenRe.MatchString(body) {
			report(lit.Pos(), "쓰기 동사 토큰 포함 문자열 리터럴 — db.Repo 단일 경로 위반")
			flagged[lit.Pos()] = true
		}
		return true
	})
	// ① 반 뒤 결합 판정(②③④⑤) 공통 자산 — 패키지 블록 const 문자열
	//    테이블(R3 상수 접기)과 var []string 리터럴 테이블(R5 슬라이스
	//    접기). 접힌 값은 결합 판정에서 Ident 해석 근거로 쓰고, 원천
	//    리터럴 위치로 중복 보고를 뺀다.
	consts := constStringTable(f)
	slices := varStringSliceTable(f, consts)
	// ② 이어붙이기 조각 조립 — 개별 리터럴에는 동사가 없어도 + 로 합쳐
	//    성립하는 쓰기 동사("UP"+"DATE …" 분할 우회)도 잡는다. R2-B 폐쇄:
	//    화이트리스트 면제는 단독 리터럴 판정(①)에만 적용 — 조립 결과
	//    판정에는 면제 피연산자 본문도 parts에 포함한다("update"+
	//    " knowledge …" 교차 우회 차단). R3 상수 접기: 피연산자 Ident가
	//    패키지 블록 const면 접힌 값이 조립에 참여한다(p+"ETE …" 형태
	//    우회 차단). 이미 ①에서 보고된 피연산자(직접·const 원천 모두)가
	//    섞인 조립은 건너뛴다(중복 보고 방지). 조립 결과 전체가 화이트
	//    리스트 본문과 정확 일치하면 면제다(①과 같은 정확 일치 원칙).
	ast.Inspect(f, func(n ast.Node) bool {
		bin, ok := n.(*ast.BinaryExpr)
		if !ok || bin.Op != token.ADD {
			return true
		}
		var parts []string
		var marked []token.Pos
		joinable := true
		ast.Inspect(bin, func(m ast.Node) bool {
			switch v := m.(type) {
			case *ast.BasicLit:
				if v.Kind != token.STRING {
					return true
				}
				if flagged[v.Pos()] {
					joinable = false
					return false
				}
				parts = append(parts, unquoteLiteral(v.Value))
				marked = append(marked, v.Pos())
			case *ast.Ident:
				ci, ok := consts[v.Name]
				if !ok {
					return true
				}
				if anyFlagged(ci.lits, flagged) {
					joinable = false
					return false
				}
				parts = append(parts, ci.val)
				marked = append(marked, ci.lits...)
			}
			return true
		})
		joined := strings.Join(parts, "")
		if joinable && len(parts) >= 2 && !writeLiteralAllow[joined] &&
			writeTokenRe.MatchString(joined) {
			report(bin.Pos(), "이어붙이기 조립 쓰기 동사(분할 우회) — db.Repo 단일 경로 위반")
			// 이 조립에 참여한 리터럴(직접·const 원천)은 처리 완료로
			// 표시(중복 보고 방지).
			for _, p := range marked {
				flagged[p] = true
			}
		}
		return true
	})
	// ③ fmt 결합 호출 — Sprintf는 형식 리터럴의 %s·%v 동사 위치에 인자를
	//    대입한 결과에서만 성립하는 쓰기 동사(Sprintf("DEL%s…","ETE") 분할
	//    우회)를 잡는다. R5 폐쇄: Sprint(피연산자 전부 문자열이면 무구분자
	//    결합 — 공란 근사)·Sprintln(공백 구분자)도 같은 결합 판정으로
	//    확장한다(fmtCombined). 중첩 fmt 결합 인자는 depth 8까지 재귀
	//    결합하고 그 외 비리터럴 인자·그 외 동사는 공란 대입(동사 경계를
	//    결합 가능성으로 보수 판정). 면제 원칙은 ②와 동일 — 단독 형식
	//    리터럴의 면제는 결합 판정에 적용되지 않고, 결합 결과 전체의
	//    화이트리스트 정확 일치만 면제다.
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		combined, ok := fmtCombined(call, consts, slices, flagged, 0)
		if !ok || writeLiteralAllow[combined] || !writeTokenRe.MatchString(combined) {
			return true
		}
		report(call.Pos(), "fmt 결합 호출 쓰기 동사(분할 우회) — db.Repo 단일 경로 위반")
		return true
	})
	// ④ strings.Join 결합 — 슬라이스 요소를 구분자와 합친 결과에서만
	//    성립하는 쓰기 동사(strings.Join([]string{"DEL","ETE…"}, "") 분할
	//    우회)도 잡는다(R3). 첫 인자는 슬라이스 리터럴([]string{…}) 또는
	//    패키지 블록 var 슬라이스 테이블의 Ident(R5 슬라이스 접기)여야
	//    정적 결합이 가능하다 — 그 외(지역 변수·함수 결과)는 건너뛴다.
	//    요소는 combineParts로 접고(리터럴·const Ident·중첩 fmt 결합, 그
	//    외 공란 보수 대입), 구분자는 리터럴 본문, 비리터럴 구분자는
	//    공란(인접 결합 가능성의 보수 근사). 면제·중복 보고 원칙은 ②와
	//    동일.
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) != 2 {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Join" {
			return true
		}
		sep := ""
		if slit, ok := call.Args[1].(*ast.BasicLit); ok && slit.Kind == token.STRING {
			sep = unquoteLiteral(slit.Value)
		}
		var joined string
		var marked []token.Pos
		joinable := false
		switch first := call.Args[0].(type) {
		case *ast.CompositeLit:
			if len(first.Elts) < 2 {
				return true
			}
			parts, marks, ok := combineParts(first.Elts, consts, slices, flagged, 0)
			joined, marked, joinable = strings.Join(parts, sep), marks, ok
		case *ast.Ident:
			si, ok := slices[first.Name]
			if !ok || len(si.vals) < 2 || anyFlagged(si.lits, flagged) {
				return true
			}
			joined, marked, joinable = strings.Join(si.vals, sep), si.lits, true
		}
		if joinable && !writeLiteralAllow[joined] &&
			writeTokenRe.MatchString(joined) {
			report(call.Pos(), "strings.Join 결합 쓰기 동사(분할 우회) — db.Repo 단일 경로 위반")
			for _, p := range marked {
				flagged[p] = true
			}
		}
		return true
	})
	// ⑤ strings.Builder 연속 WriteString 결합 — 동일 Ident 수신자에 연속한
	//    WriteString 호출 인자를 순서로 이어붙인 결과에서만 성립하는 쓰기
	//    동사(b.WriteString("DEL"); b.WriteString("ETE …") 분할 우회)도
	//    잡는다(R5). 블록 문장 목록의 연속 실행(ExprStmt)만 대상 — 다른
	//    문장·다른 수신자가 끼면 실행 단위도 끊긴 것으로 본다(분기별 상호
	//    배제 쓰기의 과잉 결합 방지). 인자는 combineParts로 접는다. 면제·
	//    중복 보고 원칙은 ②와 동일.
	ast.Inspect(f, func(n ast.Node) bool {
		blk, ok := n.(*ast.BlockStmt)
		if !ok {
			return true
		}
		for i := 0; i < len(blk.List); {
			recv, first, ok := writeStringStmt(blk.List[i])
			if !ok {
				i++
				continue
			}
			var args []ast.Expr
			for i < len(blk.List) {
				r2, c2, ok := writeStringStmt(blk.List[i])
				if !ok || r2 != recv {
					break
				}
				args = append(args, c2.Args[0])
				i++
			}
			if len(args) >= 2 {
				parts, marked, joinable := combineParts(args, consts, slices, flagged, 0)
				joined := strings.Join(parts, "")
				if joinable && !writeLiteralAllow[joined] &&
					writeTokenRe.MatchString(joined) {
					report(first.Pos(), "strings.Builder 연속 WriteString 결합 쓰기 동사(분할 우회) — db.Repo 단일 경로 위반")
					for _, p := range marked {
						flagged[p] = true
					}
				}
			}
		}
		return true
	})
	return violations
}

// sprintfCombined — Sprintf(형식, 인자…)의 정적 결합 문자열(R2-B 동사
// 분할·R3 중첩 Sprintf 판정). %s·%v에는 stringOperand로 접은 인자 값을
// 순서대로 대입하고, 그 외 동사·비접힘 인자는 공란을 대입한다(플래그·
// 폭·정밀도는 건너뛴다). 공란 대입은 인접 글자가 런타임에 결합되는
// 가능성까지 보수적으로 잡는다 — 판정 전용 근사이며 실제 포맷 출력과의
// 일치는 목적이 아니다.
func sprintfCombined(format string, args []ast.Expr, consts map[string]constInfo, slices map[string]sliceInfo, flagged map[token.Pos]bool, depth int) string {
	var b strings.Builder
	ai := 0
	nextArg := func() string {
		if ai >= len(args) {
			return ""
		}
		arg := args[ai]
		ai++
		return stringOperand(arg, consts, slices, flagged, depth)
	}
	for i := 0; i < len(format); i++ {
		if format[i] != '%' {
			b.WriteByte(format[i])
			continue
		}
		j := i + 1
		for j < len(format) && strings.IndexByte("+-#0 ", format[j]) >= 0 {
			j++
		}
		for j < len(format) && format[j] >= '0' && format[j] <= '9' {
			j++
		}
		if j < len(format) && format[j] == '.' {
			j++
			for j < len(format) && format[j] >= '0' && format[j] <= '9' {
				j++
			}
		}
		if j >= len(format) {
			b.WriteByte(format[i]) // 미완성 '%' — 나머지를 그대로 둔다
			continue
		}
		verb := format[j]
		i = j
		switch {
		case verb == '%':
			b.WriteByte('%')
		case verb == 's' || verb == 'v':
			b.WriteString(nextArg())
		default:
			nextArg() // 비문자열 동사 — 인자 소비만(공란 대입)
		}
	}
	return b.String()
}

// stringOperand — 결합 판정(③ 인자·④ 요소)용 정적 문자열 근사: 문자열
// 리터럴은 본문, 패키지 블록 const Ident는 접힌 값(원천 리터럴이 이미
// stringOperand — 결합 판정(③ 인자·④ 요소·⑤ 인자)용 정적 문자열 근사:
// 문자열 리터럴은 본문, 패키지 블록 const Ident는 접힌 값(원천 리터럴이
// 이미 보고됐으면 공란 — 중복 보고 방지), fmt 결합 호출(Sprintf·Sprint·
// Sprintln)은 depth 8까지 재귀 결합(형식·피연산자 원천이 이미 보고됐으면
// 공란 — 그 호출은 ③이 따로 잡는다), 그 외는 공란(동사 경계 결합 가능성의
// 보수 대입).
func stringOperand(e ast.Expr, consts map[string]constInfo, slices map[string]sliceInfo, flagged map[token.Pos]bool, depth int) string {
	switch v := e.(type) {
	case *ast.BasicLit:
		if v.Kind == token.STRING {
			return unquoteLiteral(v.Value)
		}
	case *ast.Ident:
		if ci, ok := consts[v.Name]; ok && !anyFlagged(ci.lits, flagged) {
			return ci.val
		}
	case *ast.CallExpr:
		if depth < 8 { // v1.0.3(R4): 2단계 이상 중첩 Sprintf 우회 폐쇄
			if combined, ok := fmtCombined(v, consts, slices, flagged, depth+1); ok {
				return combined
			}
		}
	}
	return ""
}

// fmtCombined — fmt 결합 호출의 정적 결합 근사. Sprintf는 형식 리터럴의
// %s·%v에 인자를 대입하고, Sprint는 피연산자를 무구분자로, Sprintln은
// 공백 구분자로 이어붙인다(R5: Sprint·Sprintln 확장). 결합 가능한 호출이면
// (결합 문자열, true), 아니면 ("", false) — Sprintf 비문자열 형식·
// Sprint/Sprintln 피연산자 1개(새 결합 불가)·원천 이미 보고는 false다.
func fmtCombined(call *ast.CallExpr, consts map[string]constInfo, slices map[string]sliceInfo, flagged map[token.Pos]bool, depth int) (string, bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	switch sel.Sel.Name {
	case "Sprintf":
		if len(call.Args) == 0 {
			return "", false
		}
		flit, ok := call.Args[0].(*ast.BasicLit)
		if !ok || flit.Kind != token.STRING || flagged[flit.Pos()] {
			return "", false
		}
		return sprintfCombined(unquoteLiteral(flit.Value), call.Args[1:], consts, slices, flagged, depth), true
	case "Sprint", "Sprintln":
		if len(call.Args) < 2 {
			return "", false
		}
		sep := ""
		if sel.Sel.Name == "Sprintln" {
			sep = " "
		}
		parts, _, joinable := combineParts(call.Args, consts, slices, flagged, depth)
		if !joinable {
			return "", false
		}
		return strings.Join(parts, sep), true
	}
	return "", false
}

// combineParts — 결합 피연산자 식 목록의 정적 접기: 문자열 리터럴은 본문,
// 패키지 블록 const Ident는 접힌 값, 그 외는 stringOperand(중첩 fmt 결합
// 재귀·공란 보수 대입). 원천 리터럴이 이미 보고된 피연산자가 섞이면
// (nil, nil, false) — 결합을 건너뛰어 중복 보고를 뺀다. marked는 참여한
// 원천 리터럴 위치(보고 후 처리 완료 표시용).
func combineParts(exprs []ast.Expr, consts map[string]constInfo, slices map[string]sliceInfo, flagged map[token.Pos]bool, depth int) ([]string, []token.Pos, bool) {
	var parts []string
	var marked []token.Pos
	for _, e := range exprs {
		switch v := e.(type) {
		case *ast.BasicLit:
			if v.Kind != token.STRING {
				parts = append(parts, "")
				continue
			}
			if flagged[v.Pos()] {
				return nil, nil, false
			}
			parts = append(parts, unquoteLiteral(v.Value))
			marked = append(marked, v.Pos())
		case *ast.Ident:
			if ci, ok := consts[v.Name]; ok {
				if anyFlagged(ci.lits, flagged) {
					return nil, nil, false
				}
				parts = append(parts, ci.val)
				marked = append(marked, ci.lits...)
				continue
			}
			parts = append(parts, "")
		default:
			parts = append(parts, stringOperand(e, consts, slices, flagged, depth))
		}
	}
	return parts, marked, true
}

// constInfo — 패키지 블록 const 하나의 접힌 문자열과 그 원천이 된 문자열
// 리터럴 위치들(중복 보고 방지·처리 완료 표시에 사용).
type constInfo struct {
	val  string
	lits []token.Pos
}

// constStringTable — 파일 패키지 블록의 const 선언을 스캔해 문자열로
// 접히는 이름의 테이블(R3 상수 접기). 값은 문자열 리터럴·이어붙이기
// (+ )·이미 접힌 다른 const Ident 참조까지 정의 순서와 무관하게 반복
// 접기한다(사이클·비문자열·다중 이름 사양은 등록하지 않는다).
func constStringTable(f *ast.File) map[string]constInfo {
	type pending struct {
		name string
		val  ast.Expr
	}
	var decls []pending
	for _, d := range f.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		for _, s := range gd.Specs {
			vs, ok := s.(*ast.ValueSpec)
			if !ok || len(vs.Names) != 1 || len(vs.Values) != 1 {
				continue
			}
			decls = append(decls, pending{vs.Names[0].Name, vs.Values[0]})
		}
	}
	table := map[string]constInfo{}
	for changed := true; changed; {
		changed = false
		for _, d := range decls {
			if _, done := table[d.name]; done {
				continue
			}
			if ci, ok := foldConstExpr(d.val, table); ok {
				table[d.name] = ci
				changed = true
			}
		}
	}
	return table
}

// foldConstExpr — const 값 식의 문자열 접기. 리터럴·ADD·테이블 내 Ident
// 참조만 접고 그 외(함수 호출·산술·아직 미접힘 참조)는 실패로 돌려준다.
func foldConstExpr(e ast.Expr, table map[string]constInfo) (constInfo, bool) {
	switch v := e.(type) {
	case *ast.BasicLit:
		if v.Kind == token.STRING {
			return constInfo{val: unquoteLiteral(v.Value), lits: []token.Pos{v.Pos()}}, true
		}
	case *ast.Ident:
		if ci, ok := table[v.Name]; ok {
			return ci, true
		}
	case *ast.BinaryExpr:
		if v.Op == token.ADD {
			l, ok1 := foldConstExpr(v.X, table)
			r, ok2 := foldConstExpr(v.Y, table)
			if ok1 && ok2 {
				return constInfo{val: l.val + r.val, lits: append(l.lits, r.lits...)}, true
			}
		}
	}
	return constInfo{}, false
}

// anyFlagged — 리터럴 위치 중 하나라도 이미 보고·처리 완료 표시인지.
func anyFlagged(lits []token.Pos, flagged map[token.Pos]bool) bool {
	for _, p := range lits {
		if flagged[p] {
			return true
		}
	}
	return false
}

// sliceInfo — 패키지 블록 var []string 하나의 접힌 요소 값들과 그 원천이
// 된 문자열 리터럴 위치들(중복 보고 방지·처리 완료 표시에 사용).
type sliceInfo struct {
	vals []string
	lits []token.Pos
}

// varStringSliceTable — 파일 패키지 블록의 var 선언 중 []string 리터럴
// 슬라이스를 접은 테이블(R5 슬라이스 접기). 요소 전부가 문자열로 접히는
// 경우(리터럴·constStringTable의 const Ident)만 등록한다 — 하나라도
// 비접힘이면 그 이름 자체를 등록하지 않는다(공란 오인 방지).
func varStringSliceTable(f *ast.File, consts map[string]constInfo) map[string]sliceInfo {
	table := map[string]sliceInfo{}
	for _, d := range f.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok || gd.Tok != token.VAR {
			continue
		}
		for _, s := range gd.Specs {
			vs, ok := s.(*ast.ValueSpec)
			if !ok || len(vs.Names) != 1 || len(vs.Values) != 1 {
				continue
			}
			cl, ok := vs.Values[0].(*ast.CompositeLit)
			if !ok || len(cl.Elts) == 0 || !isStringSliceType(cl.Type) {
				continue
			}
			vals := make([]string, 0, len(cl.Elts))
			var lits []token.Pos
			foldable := true
			for _, e := range cl.Elts {
				if lit, ok := e.(*ast.BasicLit); ok && lit.Kind == token.STRING {
					vals = append(vals, unquoteLiteral(lit.Value))
					lits = append(lits, lit.Pos())
					continue
				}
				if id, ok := e.(*ast.Ident); ok {
					if ci, ok := consts[id.Name]; ok {
						vals = append(vals, ci.val)
						lits = append(lits, ci.lits...)
						continue
					}
				}
				foldable = false
				break
			}
			if foldable {
				table[vs.Names[0].Name] = sliceInfo{vals: vals, lits: lits}
			}
		}
	}
	return table
}

// isStringSliceType — 식이 []string 타입 표기(길이 없는 배열·요소
// string)인지.
func isStringSliceType(e ast.Expr) bool {
	at, ok := e.(*ast.ArrayType)
	if !ok || at.Len != nil {
		return false
	}
	id, ok := at.Elt.(*ast.Ident)
	return ok && id.Name == "string"
}

// writeStringStmt — 문장이 <수신자 Ident>.WriteString(인자 1개) 호출의
// ExprStmt면 수신자 이름과 호출을 돌려준다(⑤ 연속 실행 판정용).
func writeStringStmt(s ast.Stmt) (string, *ast.CallExpr, bool) {
	es, ok := s.(*ast.ExprStmt)
	if !ok {
		return "", nil, false
	}
	call, ok := es.X.(*ast.CallExpr)
	if !ok || len(call.Args) != 1 {
		return "", nil, false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "WriteString" {
		return "", nil, false
	}
	recv, ok := sel.X.(*ast.Ident)
	if !ok {
		return "", nil, false
	}
	return recv.Name, call, true
}

// TestWriteGuardBlocksBypasses — v0.41.0 리뷰·v1.0.1 R1-D·v1.0.2 R2·
// v1.0.3 R3 회귀: 실증된 우회 기법 전수(별칭·변수 조립·다중문 리터럴·
// Sprintf 인자·동사 분할 조립·면제 리터럴 조립·Sprintf 동사 분할·중첩
// Sprintf·const 테이블 조립·strings.Join 결합)이 가드에 잡히는지 합성
// 소스로 단언(구멍 폐쇄의 영구화).
func TestWriteGuardBlocksBypasses(t *testing.T) {
	cases := []struct {
		name    string
		src     string
		wantHit bool
	}{
		{
			name: "2단계 중첩 Sprintf 조립(v1.0.3 R4)",
			src: `package ops
func f() string { return fmt.Sprintf("D%sETE FROM meta WHERE key='demo'", fmt.Sprintf("%s", fmt.Sprintf("%s", "EL"))) }`,
			wantHit: true,
		},
		{
			name: "별칭 경유 UPDATE",
			src: `package ops
func f() { q := wire.QueryOnce; q(ctx, st, "UPDATE knowledge SET status='invalidated' WHERE id=1", nil) }`,
			wantHit: true,
		},
		{
			name: "변수 조립 DELETE",
			src: `package ops
func f() string { sql := "DELETE FROM meta WHERE key='demo'"; return sql }`,
			wantHit: true,
		},
		{
			name: "var 선언 별칭 INSERT",
			src: `package ops
var q = wire.QueryOnce
func f() { q(ctx, st, "INSERT INTO meta VALUES ('k','v')", nil) }`,
			wantHit: true,
		},
		{
			name: "다중문 SQL 리터럴(SELECT 선행 — 구 가드 사각)",
			src: `package ops
func f() string { sql := "SELECT 1; DELETE FROM meta WHERE key='demo'"; return sql }`,
			wantHit: true,
		},
		{
			name: "Sprintf 인자 쓰기 토큰",
			src: `package ops
func f() string { return fmt.Sprintf("SELECT 1; DELETE FROM %s", "meta") }`,
			wantHit: true,
		},
		{
			name: "동사 분할 조립(UP+DATE)",
			src: `package ops
func f() string { return "UP" + "DATE meta SET v=1" }`,
			wantHit: true,
		},
		{
			name: "면제 리터럴 조립 우회(R2-B: update+쓰기문)",
			src: `package ops
func f() string { return "update" + " knowledge SET status='invalidated' WHERE id=1" }`,
			wantHit: true,
		},
		{
			name: "Sprintf 동사 분할(DEL%s·ETE)",
			src: `package ops
func f() string { return fmt.Sprintf("DEL%s FROM meta WHERE key='demo'", "ETE") }`,
			wantHit: true,
		},
		{
			name: "Sprintf 역방향 분할(%sETE·DEL)",
			src: `package ops
func f() string { return fmt.Sprintf("%sETE FROM meta WHERE key='demo'", "DEL") }`,
			wantHit: true,
		},
		{
			name: "중첩 Sprintf 결합(R3: 인자가 Sprintf 결과)",
			src: `package ops
func f() string { return fmt.Sprintf("DEL%s FROM meta WHERE key='demo'", fmt.Sprintf("%s", "ETE")) }`,
			wantHit: true,
		},
		{
			name: "const 테이블 조립(R3: 패키지 블록 const 접기)",
			src: `package ops
const r3Head = "DEL"
const r3Tail = "ETE FROM meta WHERE key='demo'"
func f() string { return r3Head + r3Tail }`,
			wantHit: true,
		},
		{
			name: "strings.Join 결합(R3)",
			src: `package ops
func f() string { return strings.Join([]string{"DEL", "ETE FROM meta WHERE key='demo'"}, "") }`,
			wantHit: true,
		},
		{
			name: "fmt.Sprint 무구분자 결합(R5)",
			src: `package ops
func f() string { return fmt.Sprint("DEL", "ETE FROM meta WHERE key='demo'") }`,
			wantHit: true,
		},
		{
			name: "fmt.Sprintln 공백 결합 오탐 방어(R5)",
			src: `package ops
func f() string { return fmt.Sprintln("DEL", "ETE FROM meta WHERE key='demo'") }`,
		},
		{
			name: "var 슬라이스 Join 결합(R5)",
			src: `package ops
var r5Parts = []string{"DEL", "ETE FROM meta WHERE key='demo'"}
func f() string { return strings.Join(r5Parts, "") }`,
			wantHit: true,
		},
		{
			name: "Builder 연속 WriteString 결합(R5)",
			src: `package ops
func f() string {
	var b strings.Builder
	b.WriteString("DEL")
	b.WriteString("ETE FROM meta WHERE key='demo'")
	return b.String()
}`,
			wantHit: true,
		},
		{
			name: "오탐 방어: 비SQL 안내문(화이트리스트)",
			src:  "package ops\n" + `const hint = "update-rules: 소스 flint-plugin/skills/flint 미탐지 — --src 플래그로 지정"`,
		},
		{
			name: "오탐 방어: 결합 결과가 화이트리스트 본문(정확 일치)",
			src: `package ops
func f() string { return "fix: flint up" + "date" }`,
		},
		{
			name: "오탐 방어: 쓰기 단어 없는 문자열",
			src:  "package ops\n" + `const msg = "조회 결과 없음 — 침묵 정상"`,
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
			if tc.wantHit && len(got) == 0 {
				t.Fatalf("우회 미검출 — 가드 구멍")
			}
			if !tc.wantHit && len(got) != 0 {
				t.Fatalf("오탐: %v", got)
			}
		})
	}
}

// TestWriteGuardScansSubdirectories — R1-D 회귀: 가드 루트 하위 디렉터리의
// 비테스트 .go 파일도 순회 대상이다(비재규 ReadDir은 하위 디렉터리를
// 통째로 못 보았다).
func TestWriteGuardScansSubdirectories(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "sub", "deep")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	body := "package deep\n\nfunc q() string { return \"DELETE FROM meta WHERE key='demo'\" }\n"
	if err := os.WriteFile(filepath.Join(sub, "probe.go"), []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if got := writeGuardScan(root); len(got) == 0 {
		t.Fatal("하위 디렉터리 위반 미검출 — 비재귀 순회 사각 잔존")
	} else if !strings.Contains(strings.Join(got, "\n"), filepath.Join("sub", "deep", "probe.go")) {
		t.Fatalf("위반 보고가 하위 파일 경로를 밝히지 않음: %v", got)
	}
	// testdata는 Go 관례상 배포 소스가 아니므로 면제다.
	td := filepath.Join(root, "testdata")
	if err := os.MkdirAll(td, 0o755); err != nil {
		t.Fatalf("MkdirAll testdata: %v", err)
	}
	if err := os.WriteFile(filepath.Join(td, "fixture.go"), []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile testdata: %v", err)
	}
	for _, v := range writeGuardScan(root) {
		if strings.Contains(v, "testdata") {
			t.Fatalf("testdata 오탐: %v", v)
		}
	}
}
