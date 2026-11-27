package render

import (
	"fmt"
	"go/scanner"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/query"
	"github.com/synuns/flint/lib/store"
)

func TestRenderTextJokpoint(t *testing.T) {
	if got := Text("a[flint]b"); got != "a b" {
		t.Fatalf("Text(\"a[flint]b\") = %q, want \"a b\"", got)
	}
}

func TestRenderCellsJokpoint(t *testing.T) {
	got := Cells([][]any{{1, "x[flint]y"}})
	want := [][]any{{1, "x y"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Cells = %v, want %v", got, want)
	}
}

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

	masked := Masked("token ghp_0123456789abcdefghijklmnopqrst 끝")
	if !strings.Contains(masked, "[REDACTED:") {
		t.Errorf("Masked 마커 유실: %q", masked)
	}

	got := Cells([][]any{{1, "x[flint]y"}, {2, 3.5, nil}})
	if got[0][0] != 1 || got[0][1] != "x y" {
		t.Errorf("Cells[0] = %v, want [1 x y]", got[0])
	}
	if got[1][0] != 2 || got[1][1] != 3.5 || got[1][2] != nil {
		t.Errorf("Cells[1] = %v, want [2 3.5 <nil>]", got[1])
	}
}

func TestSanitizeTextChokepoint(t *testing.T) {
	for _, v := range chokepointViolations("../..") {
		t.Errorf("%s — render 우회: render.Text/Rows/Cells 사용", v)
	}
}

func chokepointViolations(root string) []string {
	allowExact := map[string]bool{
		"lib/knowledge/write.go":  true,
		"lib/knowledge/import.go": true,
		"lib/knowledge/done.go":   true,
		"lib/db/workstate.go":     true,
		"lib/store/sanitize.go":   true,
		"lib/store/pipeline.go":   true,
	}
	allowPrefix := "lib/render/"
	fset := token.NewFileSet()
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
		var s scanner.Scanner
		s.Init(fset.AddFile(path, fset.Base(), len(data)), data, nil, scanner.ScanComments)
		for {
			pos, tok, lit := s.Scan()
			if tok == token.EOF {
				break
			}

			if tok == token.IDENT && (lit == "SanitizeText" || lit == "MaskSecrets" || lit == "MaskSecretsStrict") &&
				!allowExact[rel] && !strings.HasPrefix(rel, allowPrefix) && rel != "lib/ops/backup.go" {
				found = append(found, fmt.Sprintf("%s:%d(%s)", rel, fset.Position(pos).Line, lit))
			}
		}
		return nil
	})
	if err != nil {
		return []string{fmt.Sprintf("WalkDir: %v", err)}
	}
	return found
}

func TestSanitizeTextChokepointIndirect(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "lib", "other")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	files := map[string]string{

		"bypass.go": "package other\n\nvar leaky = store.SanitizeText\n",

		"direct.go": "package other\n\nfunc f(s string) string { return store.SanitizeText(s) }\n",

		"comment.go": "package other\n\n// SanitizeText 주석 언급은 우회가 아니다\nvar x = 1\n",

		"strslash.go": "package other\n\nvar _ = \"https://demo.invalid//\" + store.SanitizeText(leak)\n",

		"blockcomment.go": "package other\n\n/* SanitizeText 블록 주석 */\nvar y = 2\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatalf("WriteFile %s: %v", name, err)
		}
	}

	rdir := filepath.Join(root, "lib", "render")
	if err := os.MkdirAll(rdir, 0o755); err != nil {
		t.Fatalf("MkdirAll render: %v", err)
	}
	if err := os.WriteFile(filepath.Join(rdir, "render.go"),
		[]byte("package render\n\nfunc Text(s string) string { return store.SanitizeText(s) }\n"), 0o644); err != nil {
		t.Fatalf("WriteFile render.go: %v", err)
	}
	got := chokepointViolations(root)
	want := []string{"lib/other/bypass.go:3(SanitizeText)", "lib/other/direct.go:3(SanitizeText)", "lib/other/strslash.go:3(SanitizeText)"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("chokepointViolations = %v, want %v", got, want)
	}
}

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
