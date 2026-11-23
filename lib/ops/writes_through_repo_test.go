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

var writeTokenRe = regexp.MustCompile(`(?i)\b(UPDATE|DELETE|INSERT)\b`)

var writeLiteralAllow = map[string]bool{

	"update":            true,
	"fix: flint update": true,

	"fix: flint doctor --check update (refresh the version cache)": true,

	"update check: %s\n": true,

	"update check: \n": true,
	"fix: flint setup --update-rules — skill sync": true,

	"update-rules: source flint-plugin/skills/flint not found — specify it with --src": true,
	"update-rules: set FLINT_CLAUDE_HOME or HOME to locate the sync destination":       true,
	"update-rules sync failed: %w": true,

	"update-rules sync failed: ": true,

	`{"hint":"unknown keys are deprecation candidates — verify then clean up manually: DELETE FROM meta WHERE key = '...'"}`: true,
}

var writeGuardDirs = []string{".", "../knowledge"}

func unquoteLiteral(v string) string {
	if s, err := strconv.Unquote(v); err == nil {
		return s
	}
	if len(v) >= 2 && (v[0] == '`' || v[0] == '"') && v[len(v)-1] == v[0] {
		return v[1 : len(v)-1]
	}
	return v
}

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

func TestWritesThroughRepoOnly(t *testing.T) {
	for _, dir := range writeGuardDirs {
		for _, v := range writeGuardScan(dir) {
			t.Error(v)
		}
	}
}

func writeGuardViolations(fset *token.FileSet, f *ast.File) []string {
	var violations []string
	report := func(pos token.Pos, why string) {
		violations = append(violations, fmt.Sprintf("%s: %s", fset.Position(pos), why))
	}

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

	consts := constStringTable(f)
	slices := varStringSliceTable(f, consts)
	for name, si := range localSliceTable(f, consts) {
		if _, taken := slices[name]; !taken {
			slices[name] = si
		}
	}

	for name, ci := range localStringTable(f, consts, flagged) {
		if _, taken := consts[name]; !taken {
			consts[name] = ci
		}
	}

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

			for _, p := range marked {
				flagged[p] = true
			}
		}
		return true
	})

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
			b.WriteByte(format[i])
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
			nextArg()
		}
	}
	return b.String()
}

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
		if depth < 8 {
			if combined, ok := fmtCombined(v, consts, slices, flagged, depth+1); ok {
				return combined
			}

			if joined, _, _, ok := sliceCombined(v, consts, slices, flagged, depth+1); ok {
				return joined
			}
		}
	}
	return ""
}

func fmtCombined(call *ast.CallExpr, consts map[string]constInfo, slices map[string]sliceInfo, flagged map[token.Pos]bool, depth int) (string, bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	switch sel.Sel.Name {
	case "Sprintf", "Errorf":
		if len(call.Args) == 0 {
			return "", false
		}
		format, _, ok := formatOperand(call.Args[0], consts, flagged)
		if !ok {
			return "", false
		}
		return sprintfCombined(format, call.Args[1:], consts, slices, flagged, depth), true
	case "Fprintf":
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

type constInfo struct {
	val  string
	lits []token.Pos
}

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

func anyFlagged(lits []token.Pos, flagged map[token.Pos]bool) bool {
	for _, p := range lits {
		if flagged[p] {
			return true
		}
	}
	return false
}

type sliceInfo struct {
	vals []string
	lits []token.Pos
}

type localInfo struct {
	val  string
	lits []token.Pos
}

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

func isStringSliceType(e ast.Expr) bool {
	at, ok := e.(*ast.ArrayType)
	if !ok || at.Len != nil {
		return false
	}
	id, ok := at.Elt.(*ast.Ident)
	return ok && id.Name == "string"
}

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
			src:  "package ops\n" + `const hint = "update-rules: source flint-plugin/skills/flint not found — specify it with --src"`,
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
