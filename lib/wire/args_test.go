package wire

import (
	"errors"
	"strings"
	"testing"
)

func demoSpec() ArgSpec {
	return ArgSpec{
		Flags: []FlagSpec{
			{Name: "--json", Value: false},
			{Name: "--tag", Value: true},
			{Name: "--entity", Value: true, Repeat: true},
		},
		Usage:           "usage: flint demo [--json] [--tag T] [--entity E]",
		DashDash:        true,
		AllowPositional: true,
	}
}

func TestParseArgsN9DashDash(t *testing.T) {
	res, err := ParseArgs([]string{"--json", "--", "--tag", "x"}, demoSpec())
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if !res.Given("--json") {
		t.Errorf("--json 미인식")
	}
	if res.Given("--tag") {
		t.Errorf(`"--" 이후 "--tag"가 플래그로 파싱됨 — N9 위반`)
	}
	if got := res.Positional(); len(got) != 2 || got[0] != "--tag" || got[1] != "x" {
		t.Errorf("positional = %v, want [--tag x]", got)
	}

	res, err = ParseArgs([]string{"--", "--json"}, demoSpec())
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if res.Given("--json") {
		t.Errorf(`선두 "--" 이후 "--json"이 플래그로 파싱됨`)
	}
	if got := res.Positional(); len(got) != 1 || got[0] != "--json" {
		t.Errorf("positional = %v, want [--json]", got)
	}
}

func TestParseArgsValueConsumption(t *testing.T) {
	res, err := ParseArgs([]string{"--tag", "--json"}, demoSpec())
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if got := res.Value("--tag"); got != "--json" {
		t.Errorf("Value(--tag) = %q, want --json(무조건 소비)", got)
	}
	if res.Given("--json") {
		t.Errorf("--json이 값이 아니라 플래그로 파싱됨")
	}

	res, err = ParseArgs([]string{"x", "--tag"}, demoSpec())
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if !res.Given("--tag") {
		t.Errorf("trailing --tag 미인식")
	}
	if res.Value("--tag") != "" || res.HasValue("--tag") {
		t.Errorf("trailing --tag = (%q, HasValue=%v), want 빈 값·HasValue false", res.Value("--tag"), res.HasValue("--tag"))
	}

	res, err = ParseArgs([]string{"--json", "x"}, demoSpec())
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if got := res.Positional(); len(got) != 1 || got[0] != "x" {
		t.Errorf("positional = %v, want [x]", got)
	}
}

func TestParseArgsRepeat(t *testing.T) {
	res, err := ParseArgs([]string{"--tag", "a", "--tag", "b"}, demoSpec())
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if got := res.Value("--tag"); got != "b" {
		t.Errorf("Value(--tag) = %q, want b(마지막 우선)", got)
	}
	if res.Count("--tag") != 2 {
		t.Errorf("Count(--tag) = %d, want 2", res.Count("--tag"))
	}
	occs := res.Occs("--tag")
	if len(occs) != 2 || occs[0].Value != "a" || occs[1].Value != "b" {
		t.Errorf("Occs(--tag) = %+v, want [a b] 순서 보존", occs)
	}
}

func TestParseArgsUnknown(t *testing.T) {

	res, err := ParseArgs([]string{"query", "-x"}, demoSpec())
	if err == nil {
		t.Fatalf("err = nil, want unknown flag 오류(res=%+v)", res)
	}
	var ue *UsageError
	if !errors.As(err, &ue) {
		t.Errorf("오류 타입 = %T, want *UsageError", err)
	}
	want := "unknown flag: -x — usage: flint demo [--json] [--tag T] [--entity E]"
	if err.Error() != want {
		t.Errorf("message = %q, want %q", err.Error(), want)
	}
	res, err = ParseArgs([]string{"query"}, demoSpec())
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if got := res.Positional(); len(got) != 1 || got[0] != "query" {
		t.Errorf("positional = %v, want [query]", got)
	}

	strict := demoSpec()
	strict.AllowPositional = false
	if _, err := ParseArgs([]string{"foo"}, strict); err == nil {
		t.Errorf("엄격 스펙에서 무접두 토큰이 통과됨")
	}

	absorb := demoSpec()
	absorb.DashPositional = true
	res, err = ParseArgs([]string{"-x", "--y"}, absorb)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if got := res.Positional(); len(got) != 2 {
		t.Errorf("positional = %v, want 흡수 2건", got)
	}
}

func TestParseArgsDashDashOff(t *testing.T) {
	absorb := demoSpec()
	absorb.DashDash = false
	absorb.DashPositional = true
	res, err := ParseArgs([]string{"--", "x"}, absorb)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if got := res.Positional(); len(got) != 2 || got[0] != "--" {
		t.Errorf("positional = %v, want [-- x]", got)
	}

	strict := demoSpec()
	strict.DashDash = false
	strict.AllowPositional = false
	if _, err := ParseArgs([]string{"--"}, strict); err == nil {
		t.Errorf(`DashDash=false 엄격 스펙에서 "--"가 통과됨`)
	}
}

func TestParseArgsOrderAndIndex(t *testing.T) {
	res, err := ParseArgs([]string{"q", "--json", "--tag", "a", "z", "--entity", "e"}, demoSpec())
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if got := res.FlagOrder(); len(got) != 3 || got[0] != "--json" || got[1] != "--tag" || got[2] != "--entity" {
		t.Errorf("FlagOrder = %v, want [--json --tag --entity]", got)
	}
	if occ := res.Occs("--tag"); len(occ) != 1 || occ[0].Index != 2 {
		t.Errorf("Occs(--tag) = %+v, want Index 2", occ)
	}
	if pts := res.PosToks(); len(pts) != 2 || pts[0].Value != "q" || pts[0].Index != 0 || pts[1].Value != "z" || pts[1].Index != 4 {
		t.Errorf("PosToks = %+v, want q@0 z@4", pts)
	}
}

func TestMaskTokenExportedV1310(t *testing.T) {
	if got := MaskToken("postgresql://flint:pw@127.0.0.1:5434/db"); got != "postgresql://***" {
		t.Fatalf("MaskToken = %q, want postgresql://***", got)
	}
	if got := MaskToken("--plain"); got != "--plain" {
		t.Fatalf("MaskToken(--plain) = %q, want 불변", got)
	}
}

func TestUnknownFlagMasksTokensV1302(t *testing.T) {
	_, err := ParseArgs([]string{"--engine-url=postgresql://flint:pw@127.0.0.1:5434/db"}, ArgSpec{})
	if err == nil {
		t.Fatal("오류 기대")
	}
	msg := err.Error()
	if strings.Contains(msg, "flint:pw") || strings.Contains(msg, "127.0.0.1") {
		t.Fatalf("비밀/호스트 노출: %s", msg)
	}
	if !strings.Contains(msg, "://***") {
		t.Fatalf("마스킹 부재: %s", msg)
	}
}
