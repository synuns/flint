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
// v1.0.5 R6 보강(실증 우회 4종 추가 폐쇄 — 재프로브로 우회 확인 후):
//   - 이스케이프 디코딩 — unquoteLiteral을 strconv.Unquote로 교체해
//     \xNN·8진 \NNN 표기의 동사("\x44\x45\x4C…" = "DEL")도 ①~⑥·const/var
//     접기 전 공통 경로에서 디코딩해 판정한다.
//   - 문자열 누적(+=) 결합(s := "DEL"; s += "ETE …") — 블록 문장 목록의
//     같은 Ident 대입·누적 피연산자를 순서로 합친 결과로 판정한다(⑥).
//   - fmt.Errorf·fmt.Fprintf 결합 — Errorf는 Sprintf와 동일, Fprintf는
//     작성자(writer) 인자를 제외한 형식 대입 결합이다(fmtCombined).
//   - slices.Concat 결합 — 슬라이스 인자 전체 요소를 무구분자로 잇는
//     결과로 판정한다(④).
//
// v1.0.6 R7 보강(실증 우회 3종 추가 폐쇄 — 재프로브로 우회 확인 후):
//   - 형식 조립 — fmt 결합 호출의 형식 피연산자가 이어붙이기(+ ) 식이면
//     ②와 동일한 조립 접기를 적용한다(formatOperand·foldAddString —
//     Errorf("DEL"+"%s…")·Fprintf(w,"%s"+"ETE…") 폐쇄).
//   - 자기 참조 재할당 누적 — `s = s + 피연산자` 재할당을 += 와 동등한
//     누적으로 체인에 잇는다(⑥ splitSelfAdd).
//
// v1.0.7 R8 보강(실증 우회 2종 추가 폐쇄 — 재프로브로 우회 확인 후):
//   - const 형식 — fmt 결합 호출의 형식 피연산자가 패키지 블록 const
//     Ident여도 접힌 값으로 판정한다(formatOperand Ident case).
//   - 중첩 Join/Concat — ④의 결합 본체를 sliceCombined로 추출해
//     stringOperand가 depth 한계 내 재귀 접기한다(내부 결합이 무해할 때
//     외부 판정에서 공란 대입되던 우회 폐쇄).
//
// v1.0.8 R9 보강(실증 우회 3변형 추가 폐쇄 — 재프로브로 우회 확인 후):
//   - 블록 지역 값 테이블 — ⑥에 같은-블록 지역 문자열 값(locals)을
//     둔다. 종자 `x := 접힘식`을 기록하고 `y := x`·`y := x+lit`·`y += x`
//     치환을 전파한다(head+lit·복사 종자 += 폐쇄). 접힘 값 자체가 동사를
//     이루면 값 수준으로 보고한다.
//   - 지역 슬라이스 Join — sliceCombined의 Ident 해석에 함수 지역 슬라이스
//     리터럴(`parts := []string{…}`)을 병합한다(localSliceTable — 이름
//     재정의는 등록 제외로 오탐 원천 차단).
//
// v1.0.9 R10 보강(실증 우회 6변형 추가 폐쇄 — 재프로브로 우회 확인 후):
//   - 지역값 전파 — 함수 지역 문자열 값 대입을 접은 localStringTable을
//     consts 에 병합해 ②~⑤의 Ident 해석(stringOperand·combineParts·
//     formatOperand·② BinaryExpr)에 전파한다. 지역값×Sprintf/Errorf
//     인자·형식 Ident×지역·지역값×Builder·×Join/Concat 요소·×return
//     조립을 폐쇄한다. localSliceTable 과 동일 패턴(같은 이름 다중 대입
//     등록 제외)이라 타 스코프 값의 오탐은 원천 차단된다.
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
	"strconv"
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
	// 위 Errorf 안내문의 정적 결합 근사 형태(R6: %w 동사 공란 대입 결과) —
	// fmt.Errorf("update-rules 싱크 실패: %w", err)의 fmtCombined 본문.
	"update-rules 싱크 실패: ": true,
	// maintain unknown-키 힌트: 사람이 수동 실행할 문구 안내(실행 아님).
	`{"hint":"unknown 키는 사용 중단 후보 — 확인 후 수동 정리: DELETE FROM meta WHERE key = '...'"}`: true,
}

// writeGuardDirs — 가드 대상 패키지 루트(lib/ops·lib/knowledge). 하위
// 디렉터리까지 재귀 순회한다(R1-D: 비재귀 ReadDir의 하위 디렉터리 사각
// 폐쇄).
var writeGuardDirs = []string{".", "../knowledge"}

// unquoteLiteral — BasicLit.Value에서 quote를 제거하고 이스케이프(\xNN·
// 8진 \NNN·\uNNNN 등)까지 디코딩한 본문(R6: strconv.Unquote 교체 —
// "\x44\x45\x4C…" 표기 디코딩 우회 폐쇄). ①~⑤·const/var 접기 전 공통
// 경로다. 디코딩 실패 시(파서가 만들지 않는 비정형) 양끝 quote 제거로
// 근사한다.
func unquoteLiteral(v string) string {
	if s, err := strconv.Unquote(v); err == nil {
		return s
	}
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
	// ① 반 뒤 결합 판정(②~⑥) 공통 자산 — 패키지 블록 const 문자열
	//    테이블(R3 상수 접기)과 var []string 리터럴 테이블(R5 슬라이스
	//    접기)에 함수 지역 슬라이스 리터럴(R9)을 병합한다. 접힌 값은
	//    결합 판정에서 Ident 해석 근거로 쓰고, 원천 리터럴 위치로 중복
	//    보고를 뺀다.
	consts := constStringTable(f)
	slices := varStringSliceTable(f, consts)
	for name, si := range localSliceTable(f, consts) {
		if _, taken := slices[name]; !taken {
			slices[name] = si
		}
	}
	// R10: 함수 지역 문자열 값을 consts 에 병합해 ②~⑤의 Ident 해석에
	// 전파한다(지역값×fmt 인자·형식·Builder·Join 요소·return 조립 폐쇄).
	for name, ci := range localStringTable(f, consts, flagged) {
		if _, taken := consts[name]; !taken {
			consts[name] = ci
		}
	}
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
	//    확장한다(fmtCombined). R6 폐쇄: Errorf(Sprintf 동일 형식 대입)·
	//    Fprintf(작성자 인자 제외 형식 대입)도 포함한다. 중첩 fmt 결합
	//    인자는 depth 8까지 재귀 결합하고 그 외 비리터럴 인자·그 외 동사는
	//    공란 대입(동사 경계를 결합 가능성으로 보수 판정). 면제 원칙은 ②와
	//    동일 — 단독 형식 리터럴의 면제는 결합 판정에 적용되지 않고, 결합
	//    결과 전체의 화이트리스트 정확 일치만 면제다.
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
	// ④ 슬라이스 결합(strings.Join·slices.Concat) — 슬라이스 요소를 합친
	//    결과에서만 성립하는 쓰기 동사(strings.Join([]string{"DEL","ETE…"},
	//    "") 분할 우회, R3)도 잡는다. 결합 본체는 sliceCombined(요소·구분
	//    자 접기, R8 추출)이고 ④는 요소 수 ≥2의 보고 관문이다 — 1요소
	//    Join은 단독 결합이 없으므로 보고하지 않되, stringOperand 재귀에서
	//    값으로는 접힌다(중첩 Join의 내부 무해 결합 폐쇄). 면제·중복 보고
	//    원칙은 ②와 동일.
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		var what string
		switch sel.Sel.Name {
		case "Join":
			what = "strings.Join"
		case "Concat":
			what = "slices.Concat"
		default:
			return true
		}
		joined, marked, count, ok := sliceCombined(call, consts, slices, flagged, 0)
		if !ok || count < 2 || writeLiteralAllow[joined] || !writeTokenRe.MatchString(joined) {
			return true
		}
		report(call.Pos(), what+" 결합 쓰기 동사(분할 우회) — db.Repo 단일 경로 위반")
		for _, p := range marked {
			flagged[p] = true
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
	// ⑥ 문자열 누적·블록 지역 값 결합 — 블록 문장 목록에서 같은 Ident에
	//    이어지는 대입(=·:=) 종자와 += ·자기 참조 재할당(R7) 피연산자를
	//    순서로 합친 결과에서만 성립하는 쓰기 동사(s := "DEL"; s +=
	//    "ETE …" 분할 우회, R6)를 잡는다. R9 보강: 블록 지역 문자열 값
	//    테이블(locals)로 종자 `x := 접힘식`을 기록하고 `y := x`·
	//    `y := x+lit`·`y += x` 치환을 전파한다(head+lit·복사 종자 += 폐쇄)
	//    — 접힘 값 자체가 동사를 이루면 값 수준으로 보고한다. 스코프는
	//    기존 같은-블록 보수 경계를 승계한다(중첩 블록은 그 블록 순회가
	//    따로 본다 — 실소스 무해 조립의 과잉 결합 방지). 비접힘 피연산자는
	//    공란(보수), 재대입은 누적을 마치고 다시 시작한다. 면제·중복 보고
	//    원칙은 ②와 동일.
	ast.Inspect(f, func(n ast.Node) bool {
		blk, ok := n.(*ast.BlockStmt)
		if !ok {
			return true
		}
		type accum struct {
			parts  []string
			marked []token.Pos
			first  token.Pos
		}
		chain := map[string]*accum{}
		var order []*accum
		locals := map[string]localInfo{}
		flush := func(c *accum) {
			if c == nil || len(c.parts) < 2 || anyFlagged(c.marked, flagged) {
				return
			}
			joined := strings.Join(c.parts, "")
			if !writeLiteralAllow[joined] && writeTokenRe.MatchString(joined) {
				report(c.first, "문자열 누적(+=) 결합 쓰기 동사(분할 우회) — db.Repo 단일 경로 위반")
				for _, p := range c.marked {
					flagged[p] = true
				}
			}
		}
		// 값 수준 결합 판정 — 접힘 값 자체가 쓰기 동사를 이루는 조립
		// (head+lit 처럼 한 식에서 완성되는 변형).
		reportValue := func(pos token.Pos, val string, lits []token.Pos) {
			if anyFlagged(lits, flagged) || writeLiteralAllow[val] || !writeTokenRe.MatchString(val) {
				return
			}
			report(pos, "블록 지역 값 결합 쓰기 동사(분할 우회) — db.Repo 단일 경로 위반")
			for _, p := range lits {
				flagged[p] = true
			}
		}
		for _, stmt := range blk.List {
			as, ok := stmt.(*ast.AssignStmt)
			if !ok || len(as.Lhs) != 1 || len(as.Rhs) != 1 {
				continue
			}
			id, ok := as.Lhs[0].(*ast.Ident)
			if !ok {
				continue
			}
			switch as.Tok {
			case token.ASSIGN, token.DEFINE:
				// R7: `s = s + 피연산자` 자기 참조 재할당은 += 와 동등한
				// 누적으로 체인을 잇는다(자기 참조 원천은 결합에 기여하지
				// 않는다 — 체인 종자가 이미 그 값을 담는다). R9: 피연산자는
				// locals 치환으로 접고, 접힌 값은 locals 에 반영한다.
				if rest, self := splitSelfAdd(as.Rhs[0], id.Name); self {
					var vals []string
					var lits []token.Pos
					all := true
					for _, e := range rest {
						if v, ls, ok := foldLocalExpr(e, locals, consts, flagged); ok {
							vals = append(vals, v)
							lits = append(lits, ls...)
						} else {
							all = false
						}
					}
					if all {
						c := chain[id.Name]
						if c == nil {
							c = &accum{first: as.Pos()}
							chain[id.Name] = c
							order = append(order, c)
						}
						c.parts = append(c.parts, vals...)
						c.marked = append(c.marked, lits...)
						if li, ok := locals[id.Name]; ok {
							locals[id.Name] = localInfo{val: li.val + strings.Join(vals, ""), lits: append(li.lits, lits...)}
						} else {
							delete(locals, id.Name)
						}
						continue
					}
					delete(locals, id.Name)
					parts, marked, joinable := combineParts(rest, consts, slices, flagged, 0)
					c := chain[id.Name]
					if !joinable {
						flush(c)
						delete(chain, id.Name)
						continue
					}
					if c == nil {
						c = &accum{first: as.Pos()}
						chain[id.Name] = c
						order = append(order, c)
					}
					c.parts = append(c.parts, parts...)
					c.marked = append(c.marked, marked...)
					continue
				}
				flush(chain[id.Name])
				delete(chain, id.Name)
				// R9: 종자 접기 — 값을 테이블에 기록하고 값 수준 결합을
				// 판정한다. 접히지 않으면 값 추적을 끊는다(보수).
				if val, lits, ok := foldLocalExpr(as.Rhs[0], locals, consts, flagged); ok {
					reportValue(as.Pos(), val, lits)
					locals[id.Name] = localInfo{val: val, lits: lits}
					c := &accum{parts: []string{val}, marked: lits, first: as.Pos()}
					chain[id.Name] = c
					order = append(order, c)
					continue
				}
				delete(locals, id.Name)
				parts, marked, joinable := combineParts(as.Rhs, consts, slices, flagged, 0)
				if !joinable {
					continue
				}
				c := &accum{parts: parts, marked: marked, first: as.Pos()}
				chain[id.Name] = c
				order = append(order, c)
			case token.ADD_ASSIGN:
				c := chain[id.Name]
				if c == nil {
					c = &accum{first: as.Pos()}
					chain[id.Name] = c
					order = append(order, c)
				}
				// R9: 누적 피연산자가 locals 로 접히면 값·체인 모두에
				// 반영한다(복사 종자 += 전파).
				if val, lits, ok := foldLocalExpr(as.Rhs[0], locals, consts, flagged); ok {
					c.parts = append(c.parts, val)
					c.marked = append(c.marked, lits...)
					if li, ok := locals[id.Name]; ok {
						locals[id.Name] = localInfo{val: li.val + val, lits: append(li.lits, lits...)}
					}
					continue
				}
				delete(locals, id.Name)
				parts, marked, joinable := combineParts(as.Rhs, consts, slices, flagged, 0)
				if !joinable {
					flush(c)
					delete(chain, id.Name)
					continue
				}
				c.parts = append(c.parts, parts...)
				c.marked = append(c.marked, marked...)
			}
		}
		for _, c := range order {
			flush(c)
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
			// R8: 중첩 Join/Concat — 내부 결합이 무해해도 외부 판정의
			// 피연산자 값으로 접힌다(공란 대입 우회 폐쇄).
			if joined, _, _, ok := sliceCombined(v, consts, slices, flagged, depth+1); ok {
				return joined
			}
		}
	}
	return ""
}

// fmtCombined — fmt 결합 호출의 정적 결합 근사. Sprintf·Errorf는 형식
// 리터럴의 %s·%v에 인자를 대입하고(Fprintf는 작성자(writer) 인자를 제외,
// R6), Sprint는 피연산자를 무구분자로, Sprintln은 공백 구분자로 이어붙인다
// (R5: Sprint·Sprintln 확장). 결합 가능한 호출이면 (결합 문자열, true),
// 아니면 ("", false) — 비문자열 형식·Sprint/Sprintln 피연산자 1개(새 결합
// 불가)·원천 이미 보고는 false다.
func fmtCombined(call *ast.CallExpr, consts map[string]constInfo, slices map[string]sliceInfo, flagged map[token.Pos]bool, depth int) (string, bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	switch sel.Sel.Name {
	case "Sprintf", "Errorf": // R6: Errorf는 Sprintf와 동일 형식 대입 결합
		if len(call.Args) == 0 {
			return "", false
		}
		format, _, ok := formatOperand(call.Args[0], consts, flagged)
		if !ok {
			return "", false
		}
		return sprintfCombined(format, call.Args[1:], consts, slices, flagged, depth), true
	case "Fprintf": // R6: args[0]은 작성자(writer) — 결합에서 제외
		if len(call.Args) < 2 {
			return "", false
		}
		format, _, ok := formatOperand(call.Args[1], consts, flagged)
		if !ok {
			return "", false
		}
		return sprintfCombined(format, call.Args[2:], consts, slices, flagged, depth), true
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

// sliceCombined — Join/Concat 호출의 정적 결합 근사(④ 판정 본체·
// stringOperand 재귀 공유, R8 추출). Join은 구분자(리터럴 본문, 비리터럴
// 공란 근사)로 첫 인자 요소를, Concat은 무구분자로 전 인자 요소를 잇는다.
// 슬라이스 인자는 슬라이스 리터럴([]string{…}) 또는 패키지 블록 var
// 슬라이스 테이블의 Ident여야 하고, 요소는 combineParts로 접는다(리터럴·
// const Ident·중첩 fmt/Join/Concat 결합, 그 외 공란 보수 대입). 결합
// 가능하면 (결합 문자열, 원천 리터럴 위치, 요소 수, true), 비접힘 인자·
// 원천 이미 보고면 false다.
func sliceCombined(call *ast.CallExpr, consts map[string]constInfo, slices map[string]sliceInfo, flagged map[token.Pos]bool, depth int) (string, []token.Pos, int, bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", nil, 0, false
	}
	var sliceArgs []ast.Expr
	sep := ""
	switch sel.Sel.Name {
	case "Join":
		if len(call.Args) != 2 {
			return "", nil, 0, false
		}
		sliceArgs = call.Args[:1]
		if slit, ok := call.Args[1].(*ast.BasicLit); ok && slit.Kind == token.STRING {
			sep = unquoteLiteral(slit.Value)
		}
	case "Concat":
		if len(call.Args) == 0 {
			return "", nil, 0, false
		}
		sliceArgs = call.Args
	default:
		return "", nil, 0, false
	}
	var parts []string
	var marked []token.Pos
	for _, arg := range sliceArgs {
		switch a := arg.(type) {
		case *ast.CompositeLit:
			ps, marks, ok := combineParts(a.Elts, consts, slices, flagged, depth)
			if !ok {
				return "", nil, 0, false
			}
			parts = append(parts, ps...)
			marked = append(marked, marks...)
		case *ast.Ident:
			si, ok := slices[a.Name]
			if !ok || anyFlagged(si.lits, flagged) {
				return "", nil, 0, false
			}
			parts = append(parts, si.vals...)
			marked = append(marked, si.lits...)
		default:
			return "", nil, 0, false
		}
	}
	return strings.Join(parts, sep), marked, len(parts), true
}

// formatOperand — fmt 결합 호출의 형식 피연산자 접기: 문자열 리터럴은
// 본문, 이어붙이기(+ ) 식은 ②와 동일한 조립 접기(foldAddString — R7:
// Errorf("DEL"+"%s…")·Fprintf(w,"%s"+"ETE…") 형태 폐쇄), 패키지 블록
// const Ident는 접힌 값(R8: Errorf(constFmt, …) 형태 폐쇄 — foldAddString
// 과 동일 패턴), 그 외는 실패. marked는 형식 원천 리터럴 위치(원천이 이미
// 보고됐으면 실패).
func formatOperand(e ast.Expr, consts map[string]constInfo, flagged map[token.Pos]bool) (string, []token.Pos, bool) {
	switch v := e.(type) {
	case *ast.BasicLit:
		if v.Kind == token.STRING && !flagged[v.Pos()] {
			return unquoteLiteral(v.Value), []token.Pos{v.Pos()}, true
		}
	case *ast.BinaryExpr:
		return foldAddString(v, consts, flagged)
	case *ast.Ident:
		if ci, ok := consts[v.Name]; ok && !anyFlagged(ci.lits, flagged) {
			return ci.val, ci.lits, true
		}
	}
	return "", nil, false
}

// foldAddString — 이어붙이기(+ ) 식의 ②식 조립 접기: 문자열 리터럴과
// 패키지 블록 const Ident 피연산자만 순서대로 합친다. 그 외 피연산자가
// 섞이거나 원천이 이미 보고됐으면 실패 — 근사 공란 대입으로 형식의 동사
// 배치를 왜곡하지 않는다.
func foldAddString(e ast.Expr, consts map[string]constInfo, flagged map[token.Pos]bool) (string, []token.Pos, bool) {
	switch v := e.(type) {
	case *ast.BasicLit:
		if v.Kind == token.STRING {
			if flagged[v.Pos()] {
				return "", nil, false
			}
			return unquoteLiteral(v.Value), []token.Pos{v.Pos()}, true
		}
	case *ast.Ident:
		if ci, ok := consts[v.Name]; ok {
			if anyFlagged(ci.lits, flagged) {
				return "", nil, false
			}
			return ci.val, ci.lits, true
		}
	case *ast.BinaryExpr:
		if v.Op == token.ADD {
			l, lits1, ok1 := foldAddString(v.X, consts, flagged)
			r, lits2, ok2 := foldAddString(v.Y, consts, flagged)
			if ok1 && ok2 {
				return l + r, append(lits1, lits2...), true
			}
		}
	}
	return "", nil, false
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

// localInfo — 블록 지역 문자열 값 하나와 그 원천 리터럴 위치들(R9 값
// 테이블 — 종자 기록·치환 전파·중복 보고 방지에 사용).
type localInfo struct {
	val  string
	lits []token.Pos
}

// foldLocalExpr — ⑥ 블록 지역 값 테이블 locals 를 반영한 식의 문자열
// 접기: 문자열 리터럴·locals/consts Ident·이어붙이기(+ )를 재귀 접고 그
// 외(함수 호출·산술 등)는 실패한다. 원천 리터럴이 이미 보고됐으면 실패
// (중복 보고 방지).
func foldLocalExpr(e ast.Expr, locals map[string]localInfo, consts map[string]constInfo, flagged map[token.Pos]bool) (string, []token.Pos, bool) {
	switch v := e.(type) {
	case *ast.BasicLit:
		if v.Kind == token.STRING {
			if flagged[v.Pos()] {
				return "", nil, false
			}
			return unquoteLiteral(v.Value), []token.Pos{v.Pos()}, true
		}
	case *ast.Ident:
		if li, ok := locals[v.Name]; ok {
			if anyFlagged(li.lits, flagged) {
				return "", nil, false
			}
			return li.val, li.lits, true
		}
		if ci, ok := consts[v.Name]; ok {
			if anyFlagged(ci.lits, flagged) {
				return "", nil, false
			}
			return ci.val, ci.lits, true
		}
	case *ast.BinaryExpr:
		if v.Op == token.ADD {
			l, lits1, ok1 := foldLocalExpr(v.X, locals, consts, flagged)
			r, lits2, ok2 := foldLocalExpr(v.Y, locals, consts, flagged)
			if ok1 && ok2 {
				return l + r, append(lits1, lits2...), true
			}
		}
	}
	return "", nil, false
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
			vals, lits, ok := foldSliceElements(cl.Elts, consts)
			if ok {
				table[vs.Names[0].Name] = sliceInfo{vals: vals, lits: lits}
			}
		}
	}
	return table
}

// foldSliceElements — 슬라이스 리터럴 요소의 접기: 전 요소가 문자열
// 리터럴·const Ident일 때만 (값들, 원천 위치들, true)를 돌려준다.
func foldSliceElements(elts []ast.Expr, consts map[string]constInfo) ([]string, []token.Pos, bool) {
	vals := make([]string, 0, len(elts))
	var lits []token.Pos
	for _, e := range elts {
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
		return nil, nil, false
	}
	return vals, lits, true
}

// localSliceTable — 함수 지역 슬라이스 리터럴(`parts := []string{…}`·함수
// 내 var)을 접은 테이블(R9 — 지역 슬라이스 Join 폐쇄). 같은 이름이 파일
// 내 여러 번 대입되면(스코프별 재사용·재할당) 그 이름은 등록하지 않는다 —
// 이름 충돌로 타 블록 Join 이 잘못 결합되는 오탐을 원천 차단한다(보수
// 누락 허용).
func localSliceTable(f *ast.File, consts map[string]constInfo) map[string]sliceInfo {
	table := map[string]sliceInfo{}
	seen := map[string]bool{}
	dup := map[string]bool{}
	add := func(name string, rhs ast.Expr) {
		if seen[name] {
			dup[name] = true
			return
		}
		seen[name] = true
		cl, ok := rhs.(*ast.CompositeLit)
		if !ok || !isStringSliceType(cl.Type) {
			return
		}
		vals, lits, ok := foldSliceElements(cl.Elts, consts)
		if !ok {
			return
		}
		table[name] = sliceInfo{vals: vals, lits: lits}
	}
	ast.Inspect(f, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.AssignStmt:
			if (v.Tok != token.DEFINE && v.Tok != token.ASSIGN) || len(v.Lhs) != 1 || len(v.Rhs) != 1 {
				return true
			}
			if id, ok := v.Lhs[0].(*ast.Ident); ok {
				add(id.Name, v.Rhs[0])
			}
		case *ast.DeclStmt:
			gd, ok := v.Decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.VAR {
				return true
			}
			for _, s := range gd.Specs {
				vs, ok := s.(*ast.ValueSpec)
				if !ok || len(vs.Names) != 1 || len(vs.Values) != 1 {
					continue
				}
				add(vs.Names[0].Name, vs.Values[0])
			}
		}
		return true
	})
	for name := range dup {
		delete(table, name)
	}
	return table
}

// localStringTable — 함수 지역 문자열 값 대입(`x := "lit"`·`x := "a"+
// "b"`·`x := constIdent`)을 접은 파일 단위 테이블(R10 — ②~⑤ Ident 해석
// 전파). localSliceTable 과 동일 패턴: 같은 이름이 여러 번 대입되면(스코프
// 재사용·재할당·누적) 등록에서 제외해 타 스코프 값의 잘못된 결합 오탐을
// 원천 차단한다(보수 누락 허용). 접기에 실패하는 대입(함수 호출 결과 등)도
// 이름 사용으로 본다 — 이후 접힘 대입과의 충돌을 막는다.
func localStringTable(f *ast.File, consts map[string]constInfo, flagged map[token.Pos]bool) map[string]constInfo {
	table := map[string]constInfo{}
	seen := map[string]bool{}
	dup := map[string]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok || (as.Tok != token.DEFINE && as.Tok != token.ASSIGN) || len(as.Lhs) != 1 || len(as.Rhs) != 1 {
			return true
		}
		id, ok := as.Lhs[0].(*ast.Ident)
		if !ok {
			return true
		}
		if seen[id.Name] {
			dup[id.Name] = true
			return true
		}
		seen[id.Name] = true
		val, lits, ok := foldLocalExpr(as.Rhs[0], nil, consts, flagged)
		if !ok {
			return true
		}
		table[id.Name] = constInfo{val: val, lits: lits}
		return true
	})
	for name := range dup {
		delete(table, name)
	}
	return table
}

// splitSelfAdd — 이어붙이기(+ ) 재할당 식에서 피할당 Ident(name)의 자기
// 참조 등장을 걷어낸 나머지 피연산자 목록(R7: `s = s + 피연산자`의 누적
// 분해). 식에 name이 없으면 (nil, false) — 일반 대입 종자로 판정한다.
func splitSelfAdd(e ast.Expr, name string) ([]ast.Expr, bool) {
	found := false
	var rest []ast.Expr
	var walk func(e ast.Expr)
	walk = func(e ast.Expr) {
		if bin, ok := e.(*ast.BinaryExpr); ok && bin.Op == token.ADD {
			walk(bin.X)
			walk(bin.Y)
			return
		}
		if id, ok := e.(*ast.Ident); ok && id.Name == name {
			found = true
			return
		}
		rest = append(rest, e)
	}
	walk(e)
	return rest, found
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
			name: "이스케이프 디코딩 결합(R6: \\xNN)",
			src: `package ops
func f() string { return "\x44\x45\x4C\x45\x54\x45 FROM meta WHERE key='demo'" }`,
			wantHit: true,
		},
		{
			name: "문자열 누적(+=) 결합(R6)",
			src: `package ops
func f() string {
	s := "DEL"
	s += "ETE FROM meta WHERE key='demo'"
	return s
}`,
			wantHit: true,
		},
		{
			name: "오탐 방어: 무해 누적(토큰 없음 — 실소스 누적 패턴)",
			src: `package ops
func f() string {
	s := "검사 항목: 스키마·인덱스"
	s += " · "
	s += fmt.Sprintf("상한 %d건 도달", 5)
	return s
}`,
		},
		{
			name: "오탐 방어: 누적 결과가 화이트리스트 본문(정확 일치)",
			src: `package ops
func f() string {
	s := "fix: flint "
	s += "update"
	return s
}`,
		},
		{
			name: "fmt.Errorf 결합(R6)",
			src: `package ops
func f() error { return fmt.Errorf("DEL%s FROM meta WHERE key='demo'", "ETE") }`,
			wantHit: true,
		},
		{
			name: "fmt.Fprintf 결합(R6: 작성자 인자 제외)",
			src: `package ops
func f() { fmt.Fprintf(w, "%sETE FROM meta WHERE key='demo'", "DEL") }`,
			wantHit: true,
		},
		{
			name: "slices.Concat 결합(R6)",
			src: `package ops
var r6A = []string{"DEL"}
var r6B = []string{"ETE FROM meta WHERE key='demo'"}
func f() []string { return slices.Concat(r6A, r6B) }`,
			wantHit: true,
		},
		{
			name: "형식 조립 Errorf 결합(R7)",
			src: `package ops
func f() error { return fmt.Errorf("DEL"+"%s FROM meta WHERE key='demo'", "ETE") }`,
			wantHit: true,
		},
		{
			name: "형식 조립 Fprintf 결합(R7: 작성자 인자 제외)",
			src: `package ops
func f() { fmt.Fprintf(w, "%s"+"ETE FROM meta WHERE key='demo'", "DEL") }`,
			wantHit: true,
		},
		{
			name: "자기 참조 재할당 누적 결합(R7: s = s + 피연산자)",
			src: `package ops
func f() string {
	s := "DEL"
	s = s + "ETE FROM meta WHERE key='demo'"
	return s
}`,
			wantHit: true,
		},
		{
			name: "오탐 방어: 무해 재할당(누적 결과가 화이트리스트 본문)",
			src: `package ops
func f() string {
	s := "fix: flint "
	s = s + "update"
	return s
}`,
		},
		{
			name: "const 형식 결합(R8: 형식이 const Ident)",
			src: `package ops
const r8Fmt = "DEL%s FROM meta WHERE key='demo'"
func f() error { return fmt.Errorf(r8Fmt, "ETE") }`,
			wantHit: true,
		},
		{
			name: "중첩 Join 결합(R8: 내부 결합 무해)",
			src: `package ops
func f() string {
	return strings.Join([]string{strings.Join([]string{"DEL"}, ""), "ETE FROM meta WHERE key='demo'"}, "")
}`,
			wantHit: true,
		},
		{
			name: "블록 지역 종자 조립(R9: head+lit)",
			src: `package ops
func f() string {
	head := "DEL"
	s := head + "ETE FROM meta WHERE key='demo'"
	return s
}`,
			wantHit: true,
		},
		{
			name: "블록 지역 복사 종자 누적(R9: y := x 후 +=)",
			src: `package ops
func f() string {
	head := "DEL"
	s := head
	s += "ETE FROM meta WHERE key='demo'"
	return s
}`,
			wantHit: true,
		},
		{
			name: "지역 슬라이스 Join 결합(R9)",
			src: `package ops
func f() string {
	parts := []string{"DEL", "ETE FROM meta WHERE key='demo'"}
	return strings.Join(parts, "")
}`,
			wantHit: true,
		},
		{
			name: "오탐 방어: 무해 지역 조립(head+lit)",
			src: `package ops
func f() string {
	head := "가용 지식 "
	s := head + "요약 완료"
	return s
}`,
		},
		{
			name: "오탐 방어: 무해 복사 누적(면제 정확 일치)",
			src: `package ops
func f() string {
	head := "fix: flint "
	s := head
	s += "update"
	return s
}`,
		},
		{
			name: "오탐 방어: 무해 지역 슬라이스 Join",
			src: `package ops
func f() string {
	parts := []string{"kind", "topic"}
	return strings.Join(parts, ",")
}`,
		},
		{
			name: "지역값 Sprintf 인자 결합(R10)",
			src: `package ops
func f() string {
	head := "DEL"
	return fmt.Sprintf("%sETE FROM meta WHERE key='demo'", head)
}`,
			wantHit: true,
		},
		{
			name: "지역값 Errorf 인자 결합(R10)",
			src: `package ops
func f() error {
	head := "DEL"
	return fmt.Errorf("%sETE FROM meta WHERE key='demo'", head)
}`,
			wantHit: true,
		},
		{
			name: "지역값 형식 Ident 결합(R10)",
			src: `package ops
func f() error {
	format := "DEL%s FROM meta WHERE key='demo'"
	return fmt.Errorf(format, "ETE")
}`,
			wantHit: true,
		},
		{
			name: "지역값 Builder 결합(R10)",
			src: `package ops
func f() string {
	head := "DEL"
	var b strings.Builder
	b.WriteString(head)
	b.WriteString("ETE FROM meta WHERE key='demo'")
	return b.String()
}`,
			wantHit: true,
		},
		{
			name: "지역값 Join 요소 결합(R10)",
			src: `package ops
func f() string {
	head := "DEL"
	return strings.Join([]string{head, "ETE FROM meta WHERE key='demo'"}, "")
}`,
			wantHit: true,
		},
		{
			name: "지역값 return 조립 결합(R10)",
			src: `package ops
func f() string {
	head := "DEL"
	return head + "ETE FROM meta WHERE key='demo'"
}`,
			wantHit: true,
		},
		{
			name: "오탐 방어: 무해 지역값 인자(Sprintf)",
			src: `package ops
func f() string {
	name := "flint"
	return fmt.Sprintf("%s 검사 완료", name)
}`,
		},
		{
			name: "오탐 방어: 지역값 조립이 면제 본문(정확 일치)",
			src: `package ops
func f() string {
	hint := "fix: flint "
	return hint + "update"
}`,
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
