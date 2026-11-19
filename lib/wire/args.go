// args.go — a spec-table-based argument mini-parser.
// It converges the per-command manual for-switch loops (token traversal, value consumption,
// "--" handling, and the unknown-error logic duplicated across 12 places) into one
// structured result from a flag-definition table {name, takes a value, repeatable}. The parser only tokenizes — value validation (numbers,
// allowed values, empty values) stays with the caller's contract (error wording and exit codes unchanged).
//
// Preserved semantics (the migrated commands' existing version tests are the final proof):
//   - DashDash=true: everything after "--" is positional — even flag-shaped tokens
//     are not parsed as flags (the read convention). With DashDash=false, "--" is not
//     a special token (write: absorbed as positional; strict commands: unknown).
//   - Value consumption: a value flag unconditionally consumes the next token
//     (regardless of a "-" prefix or a flag name — the read --tag --json family).
//     A trailing value flag only records its occurrence (HasValue=false, Value="")
//     — the caller's empty-value validation decides the exit.
//   - Repetition: any flag's duplicate specification is not rejected; the last value
//     wins (the pre-existing behavior of every command). Flags declared Repeat=true
//     consume the full occurrence order via Occs.
//   - Unknown: unrecognized tokens get UsageErrorf("unknown flag: ...") — the ErrLine
//     classification error:"usage", caller exit 3. AllowPositional and DashPositional
//     let the spec decide positional absorption (read: only prefix-less absorbed; write: all absorbed; the doctor/report family: none).
package wire

import "strings"

// FlagSpec — one flag definition (name, takes a value, repeatable).
type FlagSpec struct {
	Name   string // the formal name including dashes, e.g. "--json"
	Value  bool   // when true, consumes the next token as its value
	Repeat bool   // the repeatable declaration (consumes the Occs order — no parse rejection)
}

// ArgSpec — the per-command argument spec table.
type ArgSpec struct {
	Flags []FlagSpec
	Usage string // the unknown-flag error suffix ("usage: flint ...") — empty means no suffix
	// when true, everything after "--" is positional (flag parsing terminates).
	DashDash bool
	// allows unrecognized tokens to be absorbed as positional. When false, even
	// prefix-less tokens are unknown-flag errors (the doctor/report/audit family).
	AllowPositional bool
	// '-'-prefixed unrecognized tokens are also absorbed as positional (the write
	// family). With AllowPositional && !DashPositional, a '-' prefix is an
	DashPositional bool
}

// ArgOcc — one flag occurrence (occurrence order and args index preserved — for
// mode election and error-order reproduction consumers).
type ArgOcc struct {
	Name     string
	Value    string // a value flag: the consumed value (\"\" if absent)
	HasValue bool   // whether a value token followed (trailing detection)
	Index    int    // the flag token's position within args
}

// PosTok — a positional token (order and index preserved — for the profile positional-value merge).
type PosTok struct {
	Value string
	Index int
}

// ArgResult — the structured parse result (an immutable value object).
type ArgResult struct {
	occ    []ArgOcc
	byName map[string][]ArgOcc
	pos    []PosTok
}

// ParseArgs — parses args against the spec table. Unrecognized tokens yield a
// *UsageError (caller convention: 1 ErrLine + exit 3).
func ParseArgs(args []string, spec ArgSpec) (*ArgResult, error) {
	flags := make(map[string]FlagSpec, len(spec.Flags))
	for _, f := range spec.Flags {
		flags[f.Name] = f
	}
	res := &ArgResult{byName: map[string][]ArgOcc{}}
	add := func(o ArgOcc) {
		res.occ = append(res.occ, o)
		res.byName[o.Name] = append(res.byName[o.Name], o)
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		// after "--" everything is positional — no further flag detection.
		if a == "--" && spec.DashDash {
			for j, p := range args[i+1:] {
				res.pos = append(res.pos, PosTok{Value: p, Index: i + 1 + j})
			}
			break
		}
		if fs, ok := flags[a]; ok {
			o := ArgOcc{Name: a, Index: i}
			if fs.Value && i+1 < len(args) {
				o.Value, o.HasValue = args[i+1], true
				i++
			}
			add(o)
			continue
		}
		if spec.AllowPositional && (!strings.HasPrefix(a, "-") || spec.DashPositional) {
			res.pos = append(res.pos, PosTok{Value: a, Index: i})
			continue
		}
		if spec.Usage != "" {
			return nil, UsageErrorf("unknown flag: %s — %s", a, spec.Usage)
		}
		return nil, UsageErrorf("unknown flag: %s", a)
	}
	return res, nil
}

// Given — whether the flag was specified at least once.
func (r *ArgResult) Given(name string) bool { return r.Count(name) > 0 }

// Count — the occurrence count (repeatable flags).
func (r *ArgResult) Count(name string) int { return len(r.byName[name]) }

// Value — the last value (inheriting the every-command last-value-wins
// semantics. Unspecified or a missing trailing value is "").
func (r *ArgResult) Value(name string) string {
	occs := r.byName[name]
	if len(occs) == 0 {
		return ""
	}
	return occs[len(occs)-1].Value
}

// HasValue — whether the last occurrence had a value token (distinguished from
// a trailing value flag — for reproducing the caller's "requires a value" error).
func (r *ArgResult) HasValue(name string) bool {
	occs := r.byName[name]
	if len(occs) == 0 {
		return false
	}
	return occs[len(occs)-1].HasValue
}

// Occs — all the flag's occurrences (order and index preserved — for Repeat-declared consumers).
func (r *ArgResult) Occs(name string) []ArgOcc { return r.byName[name] }

// AllOccs — the interleaved-order log of all flag occurrences (for consumers
// reproducing value validation in encounter order — the maintain family).
func (r *ArgResult) AllOccs() []ArgOcc { return r.occ }

// FlagOrder — the flag occurrence order log (for mode election and error-order reproduction).
func (r *ArgResult) FlagOrder() []string {
	out := make([]string, 0, len(r.occ))
	for _, o := range r.occ {
		out = append(out, o.Name)
	}
	return out
}

// PosToks — all positional tokens (order and index preserved).
func (r *ArgResult) PosToks() []PosTok { return r.pos }

// Positional — the positional values slice (order preserved).
func (r *ArgResult) Positional() []string {
	out := make([]string, 0, len(r.pos))
	for _, p := range r.pos {
		out = append(out, p.Value)
	}
	return out
}
