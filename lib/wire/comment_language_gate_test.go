package wire

import (
	"fmt"
	"go/scanner"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func hasHangul(s string) bool {
	for _, r := range s {
		if r >= 0xAC00 && r <= 0xD7A3 {
			return true
		}
	}
	return false
}

type commentLanguageViolation struct {
	Path string
	Line int
	Text string
}

func scanCommentLanguageViolations(libRoot string) ([]commentLanguageViolation, error) {
	fset := token.NewFileSet()
	var out []commentLanguageViolation
	err := filepath.WalkDir(libRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		rel, relErr := filepath.Rel(libRoot, path)
		if relErr != nil {
			return relErr
		}
		src, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		var s scanner.Scanner
		s.Init(fset.AddFile(path, fset.Base(), len(src)), src, nil, scanner.ScanComments)
		for {
			pos, tok, lit := s.Scan()
			if tok == token.EOF {
				break
			}
			if tok == token.COMMENT && hasHangul(lit) {
				p := fset.Position(pos)
				out = append(out, commentLanguageViolation{
					Path: filepath.ToSlash(rel), Line: p.Line,
					Text: strings.TrimSpace(strings.SplitN(lit, "\n", 2)[0]),
				})
			}
		}
		return nil
	})
	return out, err
}

func TestCommentLanguageSealed(t *testing.T) {
	violations, err := scanCommentLanguageViolations("..")
	if err != nil {
		t.Fatalf("comment scan failed: %v", err)
	}
	if len(violations) == 0 {
		return
	}
	var b strings.Builder
	for _, v := range violations {
		fmt.Fprintf(&b, "  %s:%d: %s\n", v.Path, v.Line, v.Text)
	}
	t.Fatalf("found %d comments with residual Hangul in lib/**.go (comments must be English):\n%s",
		len(violations), b.String())
}

func TestScanCommentLanguageClassifier(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "ops"), 0o755); err != nil {
		t.Fatal(err)
	}

	ko := "한국어"
	files := map[string]string{

		"ops/a.go": "// " + ko + " comment\nvar x = 1\n",

		"ops/b.go": "/* " + ko + " block */\nvar y = 2\n",

		"ops/c.go": "s := \"" + ko + " data\"\n",

		"ops/d.go": "s := \"a // b\"\n// english comment\n",

		"ops/e.go": "// plain english\nvar z = 3\n",

		"ops/notes.sql": "-- " + ko + " sql text\n",
		"ops/notes.txt": ko + "\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(name)), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := scanCommentLanguageViolations(root)
	if err != nil {
		t.Fatalf("scan failed: %v", err)
	}
	want := []commentLanguageViolation{
		{Path: "ops/a.go", Line: 1},
		{Path: "ops/b.go", Line: 1},
	}
	if len(got) != len(want) {
		t.Fatalf("violations = %d, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i].Path != w.Path || got[i].Line != w.Line {
			t.Errorf("violation[%d] = %s:%d, want %s:%d",
				i, got[i].Path, got[i].Line, w.Path, w.Line)
		}
	}
}
