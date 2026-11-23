//go:build ignore

package main

import (
	"flag"
	"fmt"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"

	"github.com/synuns/flint/internal/commentlint"
)

func sweep(path string, dry bool) (int, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, src, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return 0, fmt.Errorf("parse: %w", err)
	}
	type span struct{ start, end int }
	var del []span
	for _, g := range f.Comments {
		for _, c := range g.List {
			if commentlint.IsDirective(c.Text) {
				continue
			}
			del = append(del, span{fset.Position(c.Pos()).Offset, fset.Position(c.End()).Offset})
		}
	}
	if len(del) == 0 {
		return 0, nil
	}
	if !dry {
		out := src
		for i := len(del) - 1; i >= 0; i-- {
			out = append(out[:del[i].start], out[del[i].end:]...)
		}
		out, err = format.Source(out)
		if err != nil {
			return 0, fmt.Errorf("format after strip: %w", err)
		}
		if err := os.WriteFile(path, out, 0o644); err != nil {
			return 0, err
		}
	}
	return len(del), nil
}

func main() {
	root := flag.String("root", ".", "repo root")
	dry := flag.Bool("n", false, "dry run")
	flag.Parse()
	var files, comments int
	err := filepath.Walk(*root, func(path string, info os.FileInfo, werr error) error {
		if werr != nil {
			return werr
		}
		if info.IsDir() {
			switch info.Name() {
			case ".git", "vendor", "node_modules", ".superlite":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		n, serr := sweep(path, *dry)
		if serr != nil {
			return fmt.Errorf("%s: %w", path, serr)
		}
		if n > 0 {
			files++
			comments += n
			fmt.Printf("%s: %d\n", strings.TrimPrefix(path, *root+"/"), n)
		}
		return nil
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	fmt.Printf("TOTAL files=%d comments=%d\n", files, comments)
}
