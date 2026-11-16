// args_test.go — v1.6.0 T7: 사양 테이블 mini-parser 단위 계약(Review
// Focus: N9 "--" 규칙·값 소비·반복 플래그·unknown→UsageErrorf 시맨틱
// 보존). 전환 커맨드(read·write·maintain 등)의 기존 버전 테스트가
// 최종 증명이고, 여기는 파서 자체의 표를 둔다.
package wire

import (
	"errors"
	"testing"
)

// demoSpec — 표 검증용 공통 사양(read 계열 승계: N9·위치 인자 허용·
// '-' 접두 미인식은 오류).
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

// TestParseArgsN9DashDash — N9: "--" 이후 전부 위치 인자(플래그 형태
// 토큰도 플래그로 파싱하지 않는다).
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

	// 선두 "--" — 전부 위치 인자.
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

// TestParseArgsValueConsumption — 값 플래그는 다음 토큰을 무조건 소비
// (플래그형 토큰도 값이다 — read --tag --json 계열 기존 동작). trailing
// 값 플래그는 발생만 기록(값 부재 — 호출부 빈 값 검증이 맡는다).
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

	// trailing — 값 토큰 부재.
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

	// bool 플래그는 소비 없음 — 다음 토큰은 위치 인자.
	res, err = ParseArgs([]string{"--json", "x"}, demoSpec())
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if got := res.Positional(); len(got) != 1 || got[0] != "x" {
		t.Errorf("positional = %v, want [x]", got)
	}
}

// TestParseArgsRepeat — 반복 지정 시 마지막 값 우선(전 커맨드 기존
// 시맨틱). Occs는 발생 순서 전체를 보존한다(반복 플래그 소비자용).
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

// TestParseArgsUnknown — 미인식 토큰은 UsageErrorf(unknown flag). Usage
// 접미 포함, 위치 인자 허용 여부는 사양이 정한다.
func TestParseArgsUnknown(t *testing.T) {
	// read 계열: '-' 접두 미인식은 오류, 무접두는 위치 인자.
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

	// 엄격(doctor·report 계열): 무접두 토큰도 unknown flag.
	strict := demoSpec()
	strict.AllowPositional = false
	if _, err := ParseArgs([]string{"foo"}, strict); err == nil {
		t.Errorf("엄격 스펙에서 무접두 토큰이 통과됨")
	}

	// write 계열: '-' 접두 미인식도 위치 인자 흡수.
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

// TestParseArgsDashDashOff — DashDash=false면 "--"는 특별 토큰이 아니다
// (write: 위치 인자 흡수 / 엄격 커맨드: unknown flag — 기존 동작).
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

// TestParseArgsOrderAndIndex — 발생 순서·args 인덱스 보존(모드 선출·
// 오류 순서 재현·profile 위치 값 병합 소비자용).
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
