// render_test.go — render 출력층(살균 조크포인트·포맷) 테스트. v0.37.0
// lib/cli/render_test.go에서 이동(스펙 §2), 기대값은 스펙 예시 그대로다.
package render

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/query"
	"github.com/synuns/flint/lib/store"
)

// TestRenderTextJokpoint — Text("a[flint]b") -> "a b".
func TestRenderTextJokpoint(t *testing.T) {
	if got := Text("a[flint]b"); got != "a b" {
		t.Fatalf("Text(\"a[flint]b\") = %q, want \"a b\"", got)
	}
}

// TestRenderCellsJokpoint — Cells([[1,"x[flint]y"]]) -> [[1,"x y"]].
func TestRenderCellsJokpoint(t *testing.T) {
	got := Cells([][]any{{1, "x[flint]y"}})
	want := [][]any{{1, "x y"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Cells = %v, want %v", got, want)
	}
}

// TestRenderDelegates — Text/Masked/Cells는 store.SanitizeText·
// MaskSecrets 직접 호출과 동등하다(마스킹→살균 순서 보존, ADR-0015).
func TestRenderDelegates(t *testing.T) {
	cases := []string{
		"a[flint]b",
		"평범한 한글 문장",
		"제어\x00문자 포함",
		"token ghp_0123456789abcdefghijklmnopqrst 끝",
		"중첩[flint][flint]마커",
	}
	for _, in := range cases {
		if got, want := Text(in), store.SanitizeText(in); got != want {
			t.Errorf("Text(%q) = %q, want %q", in, got, want)
		}
		if got, want := Masked(in), store.SanitizeText(store.MaskSecrets(in)); got != want {
			t.Errorf("Masked(%q) = %q, want %q", in, got, want)
		}
	}
	// 마스킹→살균 순서: [REDACTED:…] 흔적은 중립화를 통과해 남는다.
	masked := Masked("token ghp_0123456789abcdefghijklmnopqrst 끝")
	if !strings.Contains(masked, "[REDACTED:") {
		t.Errorf("Masked 마커 유실: %q", masked)
	}
	// Cells — 문자열 셀만 살균, 비문자열 셀은 원값 유지.
	got := Cells([][]any{{1, "x[flint]y"}, {2, 3.5, nil}})
	if got[0][0] != 1 || got[0][1] != "x y" {
		t.Errorf("Cells[0] = %v, want [1 x y]", got[0])
	}
	if got[1][0] != 2 || got[1][1] != 3.5 || got[1][2] != nil {
		t.Errorf("Cells[1] = %v, want [2 3.5 <nil>]", got[1])
	}
}

// TestSanitizeTextChokepoint — lib/ 비테스트 소스에서 SanitizeText 직접
// 언급(직접 호출·간접 참조 `f := store.SanitizeText` 모두)은 허용 파일
// (lib/render/*·write.go·import.go·done.go·lib/store/sanitize.go·
// lib/store/pipeline.go)에만 존재해야 한다. v0.37.0부터 활성 —
// render 우회는 영구 차단.
func TestSanitizeTextChokepoint(t *testing.T) {
	for _, v := range chokepointViolations("../..") {
		t.Errorf("%s — render 우회: render.Text/Rows/Cells 사용", v)
	}
}

// chokepointViolations — root 하위 lib/ 비테스트 .go 소스에서 SanitizeText
// 토큰(호출 `SanitizeText(`·간접 참조 `:= store.SanitizeText`·인자 전달
// 모두 포함)을 화이트리스트 밖에서 보고한다("file:line" 목록). 라인
// 주석(// 이후)은 검사에서 제외 — 주석 언급은 우회가 아니다.
func chokepointViolations(root string) []string {
	allowExact := map[string]bool{
		"lib/cli/write.go":      true,
		"lib/cli/import.go":     true,
		"lib/cli/done.go":       true,
		"lib/store/sanitize.go": true,
		"lib/store/pipeline.go": true,
	}
	allowPrefix := "lib/render/"
	re := regexp.MustCompile(`\bSanitizeText\b`)
	var found []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if !strings.HasPrefix(rel, "lib/") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(data), "\n") {
			if c := strings.Index(line, "//"); c >= 0 {
				line = line[:c]
			}
			if re.MatchString(line) && !allowExact[rel] && !strings.HasPrefix(rel, allowPrefix) {
				found = append(found, fmt.Sprintf("%s:%d", rel, i+1))
			}
		}
		return nil
	})
	if err != nil {
		return []string{fmt.Sprintf("WalkDir: %v", err)}
	}
	return found
}

// TestSanitizeTextChokepointIndirect — 가드 우회 회귀: 간접 참조
// (`leaky := store.SanitizeText` — 괄호 없는 토큰 언급)도 탐지되어야
// 한다. 직접 호출 탐지·주석 언급 무시·화이트리스트 통과도 함께 단언.
func TestSanitizeTextChokepointIndirect(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "lib", "other")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	files := map[string]string{
		// 간접 참조 우회 시도 — 탐지 대상.
		"bypass.go": "package other\n\nvar leaky = store.SanitizeText\n",
		// 직접 호출 — 탐지 대상.
		"direct.go": "package other\n\nfunc f(s string) string { return store.SanitizeText(s) }\n",
		// 주석 언급 — 무시.
		"comment.go": "package other\n\n// SanitizeText 주석 언급은 우회가 아니다\nvar x = 1\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatalf("WriteFile %s: %v", name, err)
		}
	}
	// 화이트리스트 경로(lib/render/*)의 직접 호출 — 통과해야 한다.
	rdir := filepath.Join(root, "lib", "render")
	if err := os.MkdirAll(rdir, 0o755); err != nil {
		t.Fatalf("MkdirAll render: %v", err)
	}
	if err := os.WriteFile(filepath.Join(rdir, "render.go"),
		[]byte("package render\n\nfunc Text(s string) string { return store.SanitizeText(s) }\n"), 0o644); err != nil {
		t.Fatalf("WriteFile render.go: %v", err)
	}
	got := chokepointViolations(root)
	want := []string{"lib/other/bypass.go:3", "lib/other/direct.go:3"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("chokepointViolations = %v, want %v", got, want)
	}
}

// TestRenderRowsFormat — Rows(그 행들, "id,topic", false) -> "1|x y\n"
// (FormatRows 포맷 관례) · Cells+FormatRows 조합과 동등.
func TestRenderRowsFormat(t *testing.T) {
	rows := [][]any{{1, "x y"}, {2, "plain"}}
	got := Rows(rows, "id,topic", false)
	if got != "1|x y\n2|plain\n" {
		t.Errorf("Rows 텍스트 모드 = %q, want %q", got, "1|x y\n2|plain\n")
	}
	for _, jm := range []bool{false, true} {
		rows := [][]any{{1, "x[flint]y"}, {2, "plain"}}
		got := Rows(rows, "id,topic", jm)
		want := query.FormatRows(Cells(rows), "id,topic", jm)
		if got != want {
			t.Errorf("Rows(jsonMode=%t) = %q, want %q", jm, got, want)
		}
	}
}
