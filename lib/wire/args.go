// args.go — 사양 테이블 기반 인자 mini-parser.
// 커맨드별 수동 for-switch 루프(토큰 순회·값 소비·"--" 판정·unknown
// 오류의 12곳 중복)를 플래그 정의 테이블{이름·값 유무·반복 허용} →
// 구조화 결과 1값으로 수렴한다. 파서는 토큰화만 담당 — 값 검증(숫자·
// 허용값·빈 값)은 호출부 계약 그대로 남는다(오류 문구·exit 코드 불변).
//
// 보존 시맨틱(전환 커맨드 기존 버전 테스트가 최종 증명):
//   - DashDash=true: "--" 이후 전부 위치 인자 — 플래그 형태 토큰도
//     플래그로 파싱하지 않는다(read 관례). DashDash=false면 "--"는
//     특별 토큰이 아니다(write: 위치 인자 흡수·엄격 커맨드: unknown).
//   - 값 소비: 값 플래그는 다음 토큰을 무조건 소비("-" 접두·플래그명
//     여부와 무관 — read --tag --json 계열). trailing 값 플래그는 발생만
//     기록(HasValue=false, Value="") — 호출부 빈 값 검증이 exit를 정한다.
//   - 반복: 어느 플래그이든 중복 지정은 거부하지 않고 마지막 값 우선
//     (전 커맨드 기존 동작). Repeat=true 선언 플래그는 Occs로 발생
//     순서 전체를 소비한다.
//   - unknown: 미인식 토큰은 UsageErrorf("unknown flag: …") — ErrLine
//     분류 error:"usage", 호출부 exit 3. AllowPositional·DashPositional로
//     위치 인자 헡수 여부를 사양이 정한다(read: 무접두만 흡수 · write:
//     전부 흡수 · doctor·report 계열: 흡수 없음).
package wire

import "strings"

// FlagSpec — 플래그 정의 1건(이름·값 유무·반복 허용).
type FlagSpec struct {
	Name   string // "--json" — 대시 포함 정식명
	Value  bool   // 참이면 다음 토큰을 값으로 소비
	Repeat bool   // 반복 허용 선언(Occs 순서 소비 — 파싱 거부 없음)
}

// ArgSpec — 커맨드별 인자 사양 테이블.
type ArgSpec struct {
	Flags []FlagSpec
	Usage string // unknown flag 오류 접미("usage: flint …") — 빈 값이면 접미 없음
	// 참이면 "--" 이후 전부 위치 인자(플래그 파싱 종결).
	DashDash bool
	// 미인식 토큰의 위치 인자 흡수 허용. 거짓이면 무접두 토큰도 unknown
	// flag 오류(doctor·report·audit 계열).
	AllowPositional bool
	// '-' 접두 미인식 토큰도 위치 인자로 흡수(write 계열). AllowPositional
	// && !DashPositional이면 '-' 접두는 unknown flag 오류(read 계열).
	DashPositional bool
}

// ArgOcc — 플래그 발생 1건(발생 순서·args 인덱스 보존 — 모드 선출·
// 오류 순서 재현 소비자용).
type ArgOcc struct {
	Name     string
	Value    string // 값 플래그: 소비한 값(부재 "")
	HasValue bool   // 값 토큰이 뒤따랐는가(trailing 판별)
	Index    int    // 플래그 토큰의 args 내 위치
}

// PosTok — 위치 인자 토큰(순서·인덱스 보존 — profile 위치 값 병합용).
type PosTok struct {
	Value string
	Index int
}

// ArgResult — 구조화 파싱 결과(불변 값 객체).
type ArgResult struct {
	occ    []ArgOcc
	byName map[string][]ArgOcc
	pos    []PosTok
}

// ParseArgs — args를 사양 테이블로 파싱. 미인식 토큰은 *UsageError
// (호출부: ErrLine 1줄 + exit 3 관례).
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
		// "--" 이후 전부 위치 인자 — 이하 플래그 판정 없음.
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

// Given — 플래그가 1회 이상 지정됐는가.
func (r *ArgResult) Given(name string) bool { return r.Count(name) > 0 }

// Count — 발생 횟수(반복 플래그).
func (r *ArgResult) Count(name string) int { return len(r.byName[name]) }

// Value — 마지막 값(전 커맨드 마지막 값 우선 시맨틱 승계. 미지정·
// trailing 값 부재는 "").
func (r *ArgResult) Value(name string) string {
	occs := r.byName[name]
	if len(occs) == 0 {
		return ""
	}
	return occs[len(occs)-1].Value
}

// HasValue — 마지막 발생이 값 토큰을 가졌는가(trailing 값 플래그와
// 구별 — 호출부 "requires a value" 오류 재현용).
func (r *ArgResult) HasValue(name string) bool {
	occs := r.byName[name]
	if len(occs) == 0 {
		return false
	}
	return occs[len(occs)-1].HasValue
}

// Occs — 플래그 발생 전체(발생 순서·인덱스 보존 — Repeat 선언 소비자).
func (r *ArgResult) Occs(name string) []ArgOcc { return r.byName[name] }

// AllOccs — 모든 플래그 발생의 교차 순서 로그(값 검증을 인카운터 순서로
// 재현하는 소비자용 — maintain 계열).
func (r *ArgResult) AllOccs() []ArgOcc { return r.occ }

// FlagOrder — 플래그 발생 순 로그(모드 선출·오류 순서 재현용).
func (r *ArgResult) FlagOrder() []string {
	out := make([]string, 0, len(r.occ))
	for _, o := range r.occ {
		out = append(out, o.Name)
	}
	return out
}

// PosToks — 위치 인자 토큰 전체(순서·인덱스 보존).
func (r *ArgResult) PosToks() []PosTok { return r.pos }

// Positional — 위치 인자 값 슬라이스(순서 보존).
func (r *ArgResult) Positional() []string {
	out := make([]string, 0, len(r.pos))
	for _, p := range r.pos {
		out = append(out, p.Value)
	}
	return out
}
