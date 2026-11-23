package wire

import "strings"

type FlagSpec struct {
	Name   string
	Value  bool
	Repeat bool
}

type ArgSpec struct {
	Flags []FlagSpec
	Usage string

	DashDash bool

	AllowPositional bool

	DashPositional bool
}

type ArgOcc struct {
	Name     string
	Value    string
	HasValue bool
	Index    int
}

type PosTok struct {
	Value string
	Index int
}

type ArgResult struct {
	occ    []ArgOcc
	byName map[string][]ArgOcc
	pos    []PosTok
}

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

func (r *ArgResult) Given(name string) bool { return r.Count(name) > 0 }

func (r *ArgResult) Count(name string) int { return len(r.byName[name]) }

func (r *ArgResult) Value(name string) string {
	occs := r.byName[name]
	if len(occs) == 0 {
		return ""
	}
	return occs[len(occs)-1].Value
}

func (r *ArgResult) HasValue(name string) bool {
	occs := r.byName[name]
	if len(occs) == 0 {
		return false
	}
	return occs[len(occs)-1].HasValue
}

func (r *ArgResult) Occs(name string) []ArgOcc { return r.byName[name] }

func (r *ArgResult) AllOccs() []ArgOcc { return r.occ }

func (r *ArgResult) FlagOrder() []string {
	out := make([]string, 0, len(r.occ))
	for _, o := range r.occ {
		out = append(out, o.Name)
	}
	return out
}

func (r *ArgResult) PosToks() []PosTok { return r.pos }

func (r *ArgResult) Positional() []string {
	out := make([]string, 0, len(r.pos))
	for _, p := range r.pos {
		out = append(out, p.Value)
	}
	return out
}
