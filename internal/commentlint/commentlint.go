package commentlint

import (
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

func IsDirective(text string) bool {
	if strings.HasPrefix(text, "/*") {
		return false
	}
	body := strings.TrimPrefix(text, "//")
	return strings.HasPrefix(body, "go:") ||
		body == "nolint" || strings.HasPrefix(body, "nolint:") ||
		strings.HasPrefix(text, "// +build")
}

func NonDirectives(root string) ([]string, error) {
	var hits []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, werr error) error {
		if werr != nil {
			return werr
		}
		if info.IsDir() {
			if info.Name() == ".git" || info.Name() == ".superlite" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		src, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		fset := token.NewFileSet()
		f, perr := parser.ParseFile(fset, path, src, parser.ParseComments|parser.SkipObjectResolution)
		if perr != nil {
			return fmt.Errorf("%s: %w", path, perr)
		}
		for _, g := range f.Comments {
			for _, c := range g.List {
				if !IsDirective(c.Text) {
					hits = append(hits, fmt.Sprintf("%s:%d: %s", path, fset.Position(c.Pos()).Line, c.Text))
				}
			}
		}
		return nil
	})
	return hits, err
}
