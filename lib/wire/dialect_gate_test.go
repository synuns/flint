// dialect_gate_test.go — 방언 폐쇄 게이트(v1.8.0).
// lib/db 밖의 lib/**/*.go(비테스트·테스트 구분 불문)에서 Doltgres 방언
// 원시 문자열이 등장하면 실패한다. 방언 SQL은 lib/db이 유일 소유다.
// 주석 라인(트리밍 후 // 선행)과 lib/db·docs는 제외.
package wire

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// dialectPatterns — 금지 패턴. 이 파일 자신이 게이트에 걸리지 않도록
// 리터럴을 분해 조립한다(런타임에만 완성형).
func dialectPatterns() []string {
	return []string{
		"dolt_" + "log",
		"dolt_" + "diff",
		"dolt_" + "commit" + "_diff_" + "knowledge",
		"AS " + "OF '",
	}
}

// dialectViolation — 위반 한 건(상대경로·1-based 라인·해당 행).
type dialectViolation struct {
	Path string
	Line int
	Text string
}

// scanDialectViolations — libRoot(.go 패키지 디렉터리 기준 "../" = lib)의
// .go 파일을 라인 스캔해 위반을 모은다. lib/db 하위 전체는 건너뛴다.
func scanDialectViolations(libRoot string) ([]dialectViolation, error) {
	patterns := dialectPatterns()
	var out []dialectViolation
	err := filepath.WalkDir(libRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(libRoot, path)
		if relErr != nil {
			return relErr
		}
		if d.IsDir() {
			if rel == "db" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for i, line := range strings.Split(string(data), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") {
				continue
			}
			for _, p := range patterns {
				if strings.Contains(line, p) {
					out = append(out, dialectViolation{
						Path: filepath.ToSlash(rel), Line: i + 1, Text: trimmed,
					})
					break
				}
			}
		}
		return nil
	})
	return out, err
}

// TestDialectSealedOutsideDB — 게이트 본체: 위반이 1건이라도 있으면
// 파일:라인·해당 행과 함께 실패. green이면 방언 절단 완료 상태.
func TestDialectSealedOutsideDB(t *testing.T) {
	violations, err := scanDialectViolations("..")
	if err != nil {
		t.Fatalf("방언 스캔 실패: %v", err)
	}
	if len(violations) == 0 {
		return
	}
	var b strings.Builder
	for _, v := range violations {
		fmt.Fprintf(&b, "  %s:%d: %s\n", v.Path, v.Line, v.Text)
	}
	t.Fatalf("lib/db 밖 방언 원시 문자열 %d건 발견(방언은 lib/db 유일 소유):\n%s",
		len(violations), b.String())
}

// TestScanDialectViolationsClassifier — 스캐너 분류 정확성(합성 트리):
// 코드 라인의 방언 문자열(백틱 SQL·인용 문자열)은 잡고, 주석 라인과
// lib/db 하위는 허용한다.
func TestScanDialectViolationsClassifier(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "db"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "ops"), 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		// lib/db 하위 — 방언 유일 소유 영역, 전부 허용.
		"db/history.go":  "q := `SELECT * FROM " + "dolt_" + "log`\n",
		"db/diff_store.go": "q := \"... " + "dolt_" + "diff ...\"\n",
		// 주석 라인(// 선행)은 허용.
		"ops/a.go": "// 주석: " + "dolt_" + "log 언급\nvar x = 1\n",
		// 코드 라인 위반 3종(백틱 SQL·인용 문자열·AS OF 접합).
		"ops/b.go": "q := `SELECT 1 FROM " + "dolt_" + "diff`\n",
		"ops/c.go": "s := \"active_knowledge " + "AS " + "OF 'h'\"\n",
		"ops/d.go": "q := `SELECT * FROM " + "dolt_" + "commit" + "_diff_" + "knowledge`\n",
		// .go 외 파일은 무시.
		"ops/notes.txt": "raw " + "dolt_" + "log text\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(name)), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := scanDialectViolations(root)
	if err != nil {
		t.Fatalf("스캔 실패: %v", err)
	}
	want := []dialectViolation{
		{Path: "ops/b.go", Line: 1},
		{Path: "ops/c.go", Line: 1},
		{Path: "ops/d.go", Line: 1},
	}
	if len(got) != len(want) {
		t.Fatalf("위반 %d건, want %d건: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i].Path != w.Path || got[i].Line != w.Line {
			t.Errorf("위반[%d]=%s:%d, want %s:%d",
				i, got[i].Path, got[i].Line, w.Path, w.Line)
		}
	}
}
