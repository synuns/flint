// writes_through_repo_test.go — v0.41.0 T3: 쓰기 가드. lib/{ops,knowledge}
// 비테스트 소스의 Query/queryOnce 호출부에 UPDATE|DELETE|INSERT 토큰이
// 있으면 FAIL — 쓰기 SQL 리터럴은 db.Repo 단일 경로(Invalidate·Supersede·
// PruneInvalidated·MetaDelete·UpdateSessionSummary)만 소유한다.
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
	"update": true,
	"fix: flint update": true,
	"fix: flint setup --update-rules — 스킬 싱크": true,
	// rules_sync 오류 안내문.
	"update-rules: 소스 flint-plugin/skills/flint 미탐지 — --src 플래그로 지정": true,
	"update-rules: FLINT_CLAUDE_HOME 또는 HOME을 설정해 싱크 대상 경로를 지정": true,
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
	// ② 이어붙이기 조각 조립 — 개별 리터럴에는 동사가 없어도 + 로 합쳐
	//    성립하는 쓰기 동사("UP"+"DATE …" 분할 우회)도 잡는다. 피연산자
	//    중 하나라 이미 ①에서 보고됐거나 화이트리스트면 건너뛴다(중복·
	//    오탐 방지).
	ast.Inspect(f, func(n ast.Node) bool {
		bin, ok := n.(*ast.BinaryExpr)
		if !ok || bin.Op != token.ADD {
			return true
		}
		var parts []string
		joinable := true
		ast.Inspect(bin, func(m ast.Node) bool {
			lit, ok := m.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			body := unquoteLiteral(lit.Value)
			if writeLiteralAllow[body] || flagged[lit.Pos()] {
				joinable = false
				return false
			}
			parts = append(parts, body)
			return true
		})
		if joinable && len(parts) >= 2 && writeTokenRe.MatchString(strings.Join(parts, "")) {
			report(bin.Pos(), "이어붙이기 조립 쓰기 동사(분할 우회) — db.Repo 단일 경로 위반")
			// 이 조립에 참여한 리터럴은 처리 완료로 표시(중복 보고 방지).
			ast.Inspect(bin, func(m ast.Node) bool {
				if lit, ok := m.(*ast.BasicLit); ok && lit.Kind == token.STRING {
					flagged[lit.Pos()] = true
				}
				return true
			})
		}
		return true
	})
	return violations
}

// TestWriteGuardBlocksBypasses — v0.41.0 리뷰·v1.0.1 R1-D 회귀: 실증된
// 우회 기법 전수(별칭·변수 조립·다중문 리터럴·Sprintf 인자·동사 분할
// 조립)가 가드에 잡히는지 합성 소스로 단언(구멍 폐쇄의 영구화).
func TestWriteGuardBlocksBypasses(t *testing.T) {
	cases := []struct {
		name    string
		src     string
		wantHit bool
	}{
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
			name: "오탐 방어: 비SQL 안내문(화이트리스트)",
			src:  "package ops\n" + `const hint = "update-rules: 소스 flint-plugin/skills/flint 미탐지 — --src 플래그로 지정"`,
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
