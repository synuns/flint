// render_test.go — T1(render 코어·조크포인트 가드) 테스트. 기대값은
// 스펙 §2.1·§2.2 예시 그대로다.
package cli

import (
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
// 호출은 허용 파일(render.go·write.go·import.go·done.go·
// lib/store/sanitize.go·lib/store/pipeline.go)에만 존재해야 한다.
func TestSanitizeTextChokepoint(t *testing.T) {
	t.Skip("activated at T5 — T2~T4 채택 전 16곡 위반 예상")
	allow := map[string]bool{
		"lib/cli/render.go":     true,
		"lib/cli/write.go":      true,
		"lib/cli/import.go":     true,
		"lib/cli/done.go":       true,
		"lib/store/sanitize.go": true,
		"lib/store/pipeline.go": true,
	}
	re := regexp.MustCompile(`SanitizeText\(`)
	root := "../.."
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
			if re.MatchString(line) && !allow[rel] {
				t.Errorf("%s:%d — render 우회: render.Text/Rows/Cells 사용", rel, i+1)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WalkDir: %v", err)
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
