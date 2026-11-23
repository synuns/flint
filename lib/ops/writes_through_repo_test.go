// writes_through_repo_test.go — the write guard. If any non-test source in lib/{ops,knowledge}
// has an UPDATE|DELETE|INSERT token at a Query/queryOnce call site, it FAILs — within this
// guard's scope (lib/{ops,knowledge}), write SQL literals are owned only by the db.Repo paths (Invalidate,
// Supersede, PruneInvalidated,
// MetaDelete, UpdateSessionSummary). "the single db.Repo path" is a claim limited to this
// scope — outside it (e.g. the meta INSERT in lib/wire/meta.go) is separate ownership this guard
// does not see.
//
// two bypasses blocked (closed after being demonstrated):
//   - via an alias (q := wire.QueryOnce; q(..., "UPDATE ...")) — aliases holding Query/QueryOnce
//     in a variable are tracked and judged the same way.
//   - assembled into a variable (sql := "DELETE ..."; wire.QueryOnce(..., sql)) — SQL string
//     literals starting with a write verb are forbidden regardless of file position.
//
// two demonstrated bypasses closed:
//   - piece assembly and Sprintf arguments ("SELECT 1; "+"DELETE", including fmt.Sprintf) —
//     write-verb tokens are a violation anywhere in a string literal body, with no call-site or
//     literal-start position conditions (reported with location and path).
//   - split verb assembly ("UP"+"DATE ...") — write verbs that exist only in the result of
//     concatenating (+) operand literals in order are also caught.
//   - traversal blind spot — recursive WalkDir instead of non-recursive os.ReadDir (all
//     subdirectories, excluding testdata per the Go convention).
//
// two demonstrated bypasses closed:
//   - exemption x assembly crossing ("update"+" knowledge ...") — the whitelist exemption applies
//     only to the standalone-literal judgment; in the assembly (+) and Sprintf combination
//     judgments, the exempt operand bodies participate too. If the entire assembled/combined
//     result exactly matches a whitelist body, it is exempt (extending the exact-match principle
//   - Sprintf verb splitting (Sprintf("DEL%s...","ETE")) — write verbs that exist only in the
//     result of substituting string-literal arguments into the %s/%v verbs of the format literal
//     are also caught (sprintfCombined).
//
// three demonstrated bypasses closed (after re-probing confirmed the bypass):
//   - nested Sprintf (Sprintf("DEL%s...", Sprintf("%s","ETE"))) — if an argument is itself an
//     Sprintf result, it is combined recursively one level and judged (stringOperand).
//   - constant folding (const p = "DEL"; ... p+q) — the file package-block const string table
//     is folded so that Idents participate in the combination judgments (2-4) as values.
//     (constStringTable).
//   - strings.Join combination (strings.Join([]string{"DEL","ETE..."}, "")) — write verbs that
//     exist only in the result of joining literal elements with the separator are also caught (4).
//
// three demonstrated bypasses closed (after re-probing confirmed the bypass):
//   - fmt.Sprint/Sprintln combination (fmt.Sprint("DEL","ETE ...")) — not limited to Sprintf
//     format substitution; Sprint (no separator) and Sprintln (space separator) operand
//     combinations are extended into the common judgment (fmtCombined).
//   - var slice folding (var parts = []string{"DEL","ETE..."} + Join) — the package-block var
//     []string literal table is folded to resolve the Join first argument Ident
//     (varStringSliceTable).
//   - strings.Builder combination (b.WriteString("DEL"); b.WriteString("ETE ...")) — write
//     verbs that exist only in the result of concatenating, on the same Ident receiver,
//     consecutive WriteString arguments are also caught (5).
//
// four demonstrated bypasses closed (after re-probing confirmed the bypass):
//   - escape decoding — unquoteLiteral is replaced with strconv.Unquote so that \\xNN and
//     octal \\NNN verb notations ("\\x44\\x45\\x4C..." = "DEL") are decoded and judged in
//     the common path before the foldings.
//   - string accumulation (+=) combination (s := "DEL"; s += "ETE ...") — judged by merging,
//     in order, the same-Ident assignment and accumulation operands (6).
//   - fmt.Errorf and fmt.Fprintf combination — Errorf is the same as Sprintf; Fprintf is a
//     format-substitution combination excluding the writer argument (fmtCombined).
//   - slices.Concat combination — judged by concatenating all elements of the slice arguments
//     with no separator (4).
//
// three demonstrated bypasses closed (after re-probing confirmed the bypass):
//   - format assembly — if the format operand of a fmt combination call is a concatenation (+)
//     expression, the same assembly folding as 2 is applied (formatOperand, foldAddString —
//     closing Errorf("DEL"+"%s...") and Fprintf(w,"%s"+"ETE...")).
//   - self-referencing reassignment accumulation — `s = s + operand` reassignment is chained
//     into the accumulation as equivalent to += (6, splitSelfAdd).
//
// two demonstrated bypasses closed (after re-probing confirmed the bypass):
//   - const format — even when the format operand of a fmt combination call is a package-block
//     const Ident, it is judged by the folded value (the formatOperand Ident case).
//   - nested Join/Concat — the combination body of 4 is extracted as sliceCombined so that
//     stringOperand folds recursively within a depth limit (closing the bypass where an inner,
//     harmless combination was substituted as blank in the outer judgment).
//
// three demonstrated bypass variants closed (after re-probing confirmed the bypass):
//   - block-local value table — 6 keeps same-block local string values (locals). Seed `x :=
//     folded-expr` assignments are recorded, and `y := x`, `y := x+lit`, `y += x` substitutions
//     are propagated (closing head+lit and copy-seed +=). If a folded value itself forms a
//     verb, it is reported at the value level.
//   - local slice Join — sliceCombined's Ident resolution merges function-local slice
//     literals (`parts := []string{...}`) (localSliceTable — name redefinitions are excluded
//     from registration, cutting false positives at the source).
//
// six demonstrated bypass variants closed (after re-probing confirmed the bypass):
//   - local-value propagation — localStringTable, which folds function-local string value
//     assignments, is merged into consts and propagated into the 2-5 Ident resolutions
//     (stringOperand, combineParts, formatOperand, and 2's BinaryExpr). Local-value x
//     Sprintf/Errorf arguments, format Ident x local, local value x Builder, x Join/Concat
//     elements, and x return assemblies are closed. The same pattern as localSliceTable
//     (excluding multi-assignment names), so false positives from other scopes' values are cut
//
// False positives (non-SQL "update" words in guidance texts and check names) are exempted only
// by exact matches in the writeLiteralAllow whitelist — every new exemption must be an explicit
// declaration in that table.
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

// writeTokenRe — the SQL write verbs (word boundaries — "deleted", "updated_at",
// "would_delete" and other column/alias names do not match).
var writeTokenRe = regexp.MustCompile(`(?i)\b(UPDATE|DELETE|INSERT)\b`)

// writeLiteralAllow — the false-positive whitelist: exact body matches of guidance texts and
// check-name literals that contain write-verb words but are not SQL (a token anywhere is a
// violation). For legitimate sources to pass, declaration in this table is the only exemption
// path — update it together with any body edit (no automatic exemption).
var writeLiteralAllow = map[string]bool{
	// doctor check names and prescription wordings (human-facing guidance, non-SQL).
	"update":            true,
	"fix: flint update": true,
	// the v1.26.0 doctor versions stale-cache guidance (names the update check
	// — the check name itself, not SQL).
	"fix: flint doctor --check update (refresh the version cache)": true,
	// the report --text update-check freshness line (v1.26.0 T3 — display only).
	"update check: %s\n": true,
	// the static-combination approximation of the Fprintf line above (the result of blank-
	// substituting the %s verb) — the fmtCombined body.
	"update check: \n": true,
	"fix: flint setup --update-rules — skill sync": true,
	// rules_sync error guidance texts.
	"update-rules: source flint-plugin/skills/flint not found — specify it with --src": true,
	"update-rules: set FLINT_CLAUDE_HOME or HOME to locate the sync destination":       true,
	"update-rules sync failed: %w": true,
	// the static-combination approximation of the Errorf guidance above (the result of blank-
	// substituting the %w verb) — the fmtCombined body of fmt.Errorf("update-rules sync failed: %w", err).
	"update-rules sync failed: ": true,
	// the maintain unknown-key hint: wording a human runs manually (not an execution).
	`{"hint":"unknown keys are deprecation candidates — verify then clean up manually: DELETE FROM meta WHERE key = '...'"}`: true,
}

// writeGuardDirs — the package roots under guard (lib/ops and lib/knowledge). Traversed
// recursively down to subdirectories (closing the subdirectory blind spot of non-recursive
// ReadDir).
var writeGuardDirs = []string{".", "../knowledge"}

// unquoteLiteral — removes the quotes from a BasicLit.Value and returns the body with escapes (\\xNN,
// octal \\NNN, \\uNNNN, etc.) decoded (replacing strconv.Unquote —
// closing the escape-notation decoding bypass of "\\x44\\x45\\x4C..." forms). A common path before the 1-5 and const/var
// foldings. On decode failure (malformed forms the parser would not produce), stripping the surrounding quotes
// approximates it.
func unquoteLiteral(v string) string {
	if s, err := strconv.Unquote(v); err == nil {
		return s
	}
	if len(v) >= 2 && (v[0] == '`' || v[0] == '"') && v[len(v)-1] == v[0] {
		return v[1 : len(v)-1]
	}
	return v
}

// writeGuardScan — recursively walks (WalkDir) the dir root and collects guard violations across all non-test .go
// files (violation strings are self-identifying in the "path:line:col: reason" form,
// disclosing their own path).
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

// TestWritesThroughRepoOnly — zero guard violations (all sources, subdirectories
// included). A write-verb token appearing in a string literal (including pieces and Sprintf arguments,
// regardless of position) or in a concatenation result FAILs with its location.
func TestWritesThroughRepoOnly(t *testing.T) {
	for _, dir := range writeGuardDirs {
		for _, v := range writeGuardScan(dir) {
			t.Error(v)
		}
	}
}

// writeGuardViolations — the guard violation list for one parsed file (location-bearing
// strings). The TestWritesThroughRepoOnly body and the bypass regression tests share this
// judgment logic.
func writeGuardViolations(fset *token.FileSet, f *ast.File) []string {
	var violations []string
	report := func(pos token.Pos, why string) {
		violations = append(violations, fmt.Sprintf("%s: %s", fset.Position(pos), why))
	}
	// 1. all string literals — a write-verb word token anywhere in the body is a violation
	//    (no distinction between call arguments, variable assembly, or Sprintf arguments). Only exact
	//    whitelist body matches are exempt.
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
	//    common assets for the combination judgments (2-6) after pass 1 — the package-block const string
	//    table (constant folding) and the var []string literal table (slice
	//    folding), with function-local slice literals merged in. Folded values are
	//    the Ident-resolution basis in the combination judgments, and source-literal positions remove
	//    duplicate reports.
	consts := constStringTable(f)
	slices := varStringSliceTable(f, consts)
	for name, si := range localSliceTable(f, consts) {
		if _, taken := slices[name]; !taken {
			slices[name] = si
		}
	}
	// function-local string values are merged into consts and propagated into the 2-5 Ident resolutions
	// (closing local-value x fmt arguments/formats/Builder/Join elements/return assemblies).
	for name, ci := range localStringTable(f, consts, flagged) {
		if _, taken := consts[name]; !taken {
			consts[name] = ci
		}
	}
	// 2. concatenation piece assembly — even if the individual literals carry no verb, write verbs that exist
	//    only in the + concatenation (the "UP"+"DATE ..." split bypass) are caught. Closure:
	//    the whitelist exemption applies only to the standalone-literal judgment (1) — for the assembly
	//    judgment, the exempt operand bodies also join parts (blocking the "update"+
	//    " knowledge ..." crossing). Constant folding: if an operand Ident is a
	//    package-block const, its folded value joins the assembly (blocking the p+"ETE ..." form
	//    of bypass). Operands already reported in 1 (both direct and const-sourced) mixed
	//    into an assembly are skipped (avoiding duplicate reports). If the whole assembled result
	//    exactly matches a whitelist body, it is exempt (the same exact-match principle as 1).
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
			// the literals that participated in this assembly (direct and const-sourced) are marked as
			// handled (avoiding duplicate reports).
			for _, p := range marked {
				flagged[p] = true
			}
		}
		return true
	})
	// 3. fmt combination calls — Sprintf catches write verbs that exist only in the result of
	//    substituting arguments into the %s/%v verbs of the format literal (the Sprintf("DEL%s...","ETE") split
	//    bypass). Closure: Sprint (no separator when all operands are strings — blank
	//    approximation) and Sprintln (space separator) are extended into the same combination
	//    judgment (fmtCombined). Errorf (the same format substitution as Sprintf) and
	//    Fprintf (format substitution excluding the writer argument) are included. Nested fmt combination
	//    arguments are recursively combined up to depth 8; other non-literal arguments and other verbs are
	//    blank-substituted (conservatively treating verb boundaries as combinable). The exemption principle is the same as 2:
	//    identical — a standalone format literal's exemption does not apply to the combination judgment, and only an
	//    exact whitelist match of the whole combination result is exempt.
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
	// 4. slice combinations (strings.Join, slices.Concat) — write verbs that exist only in the result of joining slice
	//    elements (the strings.Join([]string{"DEL","ETE..."},
	//    "") split bypass, R3) are caught. The combination body is sliceCombined (element and separator
	//    folding), and 4 is the reporting gate for element counts >= 2 — a 1-element
	//    Join has no standalone combination and is not reported, but in the stringOperand recursion
	//    it still folds as a value (closing harmless inner combinations of nested Joins). The exemption and duplicate-report
	//    principles are the same as 2.
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
	// 5. strings.Builder consecutive WriteString combination — on the same Ident receiver, write verbs that exist
	//    only in the result of concatenating consecutive WriteString call arguments in order (the
	//    b.WriteString("DEL"); b.WriteString("ETE ...") split bypass) are also
	//    caught (R5). Only consecutive runs (ExprStmt) in the block statement list are considered — a different
	//    statement or a different receiver breaks the run unit (preventing over-combination of
	//    mutually exclusive writes across branches). Arguments are folded with combineParts. The exemption and
	//    duplicate-report principles are the same as 2.
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
	// 6. string accumulation and block-local value combination — write verbs that exist, for the same Ident in the
	//    block statement list, only in the result of merging in order the assignment (=/:=) seeds and the += and self-referencing reassignment (R7) operands
	//    (the s := "DEL"; s +=
	//    "ETE ..." split bypass) are caught. The block-local string value
	//    table (locals) records `x := folded-expr` seeds and propagates `y := x`,
	//    `y := x+lit`, `y += x` substitutions (closing head+lit and copy-seed +=)
	//    — if a folded value itself forms a verb, it is reported at the value level. The scope
	//    inherits the existing same-block conservative boundary (nested blocks are traversed
	//    separately — preventing over-combination of harmless real-source assemblies). Non-folded operands are
	//    blank (conservative); reassignment finishes the accumulation and starts over. The exemption and duplicate-report
	//    principles are the same as 2.
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
		// value-level combination judgment — assemblies whose folded value itself forms a write verb
		// (variants completing within one expression, like head+lit).
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
				// a `s = s + operand` self-referencing reassignment is chained into the accumulation as
				// equivalent to += (the self-reference does not contribute to the combination —
				// the chain seed already holds that value). Operands are
				// folded via locals substitution, and folded values are reflected into locals.
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
				// seed folding — records the value into the table and runs the value-level combination
				// judgment. If it does not fold, value tracking ends (conservative).
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
				// when an accumulation operand folds via locals, it is reflected in both the value and
				// the chain (copy-seed += propagation).
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

// sprintfCombined — the static combination string of Sprintf(format, args...) (verb
// splitting and nested-Sprintf judgment). The %s and %v verbs receive the argument values folded by stringOperand, in
// order; other verbs and non-folded arguments are blank-substituted (flags,
// width, and precision are skipped). The blank substitution conservatively catches even
// the possibility of adjacent characters combining at runtime — a judgment-only approximation, not meant to match actual format
// output.
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
			b.WriteByte(format[i]) // an unfinished '%' — leave the rest as-is
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
			nextArg() // a non-string verb — consume the argument only (blank substitution)
		}
	}
	return b.String()
}

// stringOperand — a static string approximation for the combination judgments (3's arguments, 4's elements): a string
// literal gives the body; a package-block const Ident gives the folded value (with the source literal already
// stringOperand — a static string approximation for the combination judgments (3's arguments, 4's elements, 5's arguments):
// string literals give the body; package-block const Idents give the folded value (with the source literal
// already reported — avoiding duplicates); fmt combination calls (Sprintf, Sprint,
// Sprintln) are recursively combined up to depth 8 (blank if the format or operand sources were already reported
// — that call is caught separately by 3); everything else is blank (the conservative substitution of
// combinable verb boundaries).
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
		if depth < 8 { // closing 2+-level nested Sprintf bypasses
			if combined, ok := fmtCombined(v, consts, slices, flagged, depth+1); ok {
				return combined
			}
			// nested Join/Concat — even when the inner combination is harmless, it folds into the
			// operand value of the outer judgment (closing the blank-substitution bypass).
			if joined, _, _, ok := sliceCombined(v, consts, slices, flagged, depth+1); ok {
				return joined
			}
		}
	}
	return ""
}

// fmtCombined — the static combination approximation of fmt combination calls. Sprintf and Errorf substitute arguments into the format
// literal's %s and %v (Fprintf excludes the writer argument,
// R6); Sprint concatenates the operands with no separator, Sprintln with a space separator
// (the Sprint/Sprintln extension). If the call is combinable, it returns (the combination string, true);
// otherwise ("", false) — a non-string format, a single Sprint/Sprintln operand (no new combination
// possible), or an already-reported source is false.
func fmtCombined(call *ast.CallExpr, consts map[string]constInfo, slices map[string]sliceInfo, flagged map[token.Pos]bool, depth int) (string, bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	switch sel.Sel.Name {
	case "Sprintf", "Errorf": // Errorf is the same format-substitution combination as Sprintf
		if len(call.Args) == 0 {
			return "", false
		}
		format, _, ok := formatOperand(call.Args[0], consts, flagged)
		if !ok {
			return "", false
		}
		return sprintfCombined(format, call.Args[1:], consts, slices, flagged, depth), true
	case "Fprintf": // args[0] is the writer — excluded from the combination
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

// sliceCombined — the static combination approximation of Join/Concat calls (the 4 judgment body,
// shared by the stringOperand recursion). Join joins the first argument's elements with the separator (literal body, non-literal
// approximated as blank); Concat joins all argument elements with no separator.
// The slice argument must be a slice literal ([]string{...}) or a package-block var
// slice-table Ident, and the elements are folded with combineParts (literals,
// const Idents, nested fmt/Join/Concat combinations; everything else blank, conservatively). If combinable
// it returns (the combination string, source-literal positions, element count, true); non-foldable arguments or
// already-reported sources are false.
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

// formatOperand — folds the format operand of a fmt combination call: a string literal gives
// the body; a concatenation (+) expression gets the same assembly folding as 2 (foldAddString —
// closing the Errorf("DEL"+"%s...") and Fprintf(w,"%s"+"ETE...") forms); a package-block
// const Ident gives the folded value (closing the Errorf(constFmt, ...) form — the same pattern as
// foldAddString); everything else fails. marked is the format source literal's position (already reported
// means failure).
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

// foldAddString — the style-2 assembly folding of a concatenation (+) expression: string literals and
// package-block const Ident operands are merged in order. Any other operand mixed
// in, or an already-reported source, fails — approximate blank substitution must not distort the format's verb
// placement.
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

// combineParts — the static folding of a list of combination operand expressions: string literals give the body,
// package-block const Idents give the folded value, everything else goes through stringOperand (nested fmt combination
// recursion, conservative blank substitution). If an operand whose source literal was already reported is mixed in,
// it returns (nil, nil, false) — the combination is skipped, removing duplicate reports. marked is the
// participating source-literal positions (for marking handled after reporting).
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

// constInfo — one package-block const's folded string and the string literals that sourced it
// (used for duplicate prevention and handled marking).
type constInfo struct {
	val  string
	lits []token.Pos
}

// constStringTable — scans a file's package-block const declarations and builds a table of names foldable to
// strings (constant folding). Values are string literals, concatenations
// (+), and references to already-folded const Idents — folded iteratively regardless of
// definition order (cycles, non-strings, and multi-name specs are not registered).
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

// foldConstExpr — folds a const value expression to a string. Only literals, ADD, and in-table Ident
// references fold; the rest (function calls, arithmetic, not-yet-folded references) return failure.
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

// anyFlagged — whether any of the literal positions is already reported or marked handled.
func anyFlagged(lits []token.Pos, flagged map[token.Pos]bool) bool {
	for _, p := range lits {
		if flagged[p] {
			return true
		}
	}
	return false
}

// sliceInfo — one package-block var []string's folded element values and the string literals that became
// their sources (used for duplicate prevention and handled marking).
type sliceInfo struct {
	vals []string
	lits []token.Pos
}

// localInfo — one block-local string value and its source-literal positions (a value
// table — used for seed recording, substitution propagation, and duplicate prevention).
type localInfo struct {
	val  string
	lits []token.Pos
}

// foldLocalExpr — the string folding of an expression reflecting the 6 block-local value table
// locals: string literals, locals/consts Idents, and concatenations (+) fold recursively; everything
// else (function calls, arithmetic, etc.) fails. If a source literal was already reported, it fails
// (duplicate prevention).
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

// varStringSliceTable — folds the []string literal
// slice var declarations of a file's package block into a table (slice folding). Registered only when every element folds to a string
// (literals, constStringTable const Idents) — if even one does not fold,
// the name itself is not registered (avoiding blank misreads).
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

// foldSliceElements — folds slice literal elements: every element must be a string
// literal or const Ident to return (values, source positions, true).
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

// localSliceTable — folds function-local slice literals (`parts := []string{...}`, in-function
// vars) into a table (closing local-slice Joins). If the same name is assigned
// more than once in a file (per-scope reuse or reassignment), that name is not registered —
// cutting at the source the false positives of another block's Join combining wrongly on a name collision (tolerating conservative
// misses).
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

// localStringTable — a file-wide table folding function-local string value assignments (`x := "lit"`, `x := "a"+
// "b"`, `x := constIdent`) for propagating into the 2-5 Ident resolutions
// (the same pattern as localSliceTable: names assigned more than once (per-scope
// reuse, reassignment, accumulation) are excluded from registration, cutting at the source the false positives of wrongly combining another scope's values
// (tolerating conservative misses). Assignments that fail to fold (function call results, etc.) also count as
// uses of the name — preventing collisions with later folded assignments.
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

// splitSelfAdd — from a concatenation (+) reassignment expression, the assigned Ident's (name) self-
// references are stripped, leaving the remaining operand list (the accumulation decomposition of `s = s + operand`).
// If name does not appear in the expression, (nil, false) — it is treated as an ordinary assignment seed.
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

// isStringSliceType — whether the expression is a []string type notation (array without length, string
// element).
func isStringSliceType(e ast.Expr) bool {
	at, ok := e.(*ast.ArrayType)
	if !ok || at.Len != nil {
		return false
	}
	id, ok := at.Elt.(*ast.Ident)
	return ok && id.Name == "string"
}

// writeStringStmt — if a statement is the ExprStmt of a <receiver Ident>.WriteString(1 argument) call,
// it returns the receiver name and the call (for the 5 consecutive-run judgment).
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

// TestWriteGuardBlocksBypasses — a full regression of demonstrated bypass techniques (aliases, variable
// assembly, multi-statement literals, Sprintf arguments, split-verb assembly, exempt-literal assembly,
// Sprintf verb splitting, nested Sprintf, const-table assembly, strings.Join combination) —
// asserted with synthetic sources that the guard catches them (permanizing the hole closures).
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

// TestWriteGuardScansSubdirectories — non-test .go files in subdirectories under the guard
// roots are also traversal targets (non-recursive ReadDir missed subdirectories
// entirely).
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
	// testdata is exempt per the Go convention (not shipped source).
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
