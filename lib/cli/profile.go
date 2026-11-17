// profile.go — 노출 프로파일: meta exposure_profile 판독.
// 프로파일은 hot·nudge 동작만 분기한다(다른 명령·exit 계약 불변).
// 프로파일 판정 — 추천에서 전환까지. profile_hint
// (report)가 안내만 하던 것을 실행 가능 회로로 완결한다. 판정 규칙은
// profileHint와 단일 소스(표본 하한 zero_near_min_read·recall_led_pct
// 50 기준 — METRICS §3 문서화). 근거 지표를 함께 노출해 사용자가 판정을
// 검증할 수 있게 한다(관측 투명성 — GSM 원칙).
// 판독 헬퍼(ExposureKey·ExposureProfile)는 lib/wire/profile.go
// 로 이동 — 이 파일은 판정 명령 runProfile과 cli 호환 별칭을 소유한다.
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

// 별칭 — wire 이동분 재노출(setup.go 기록 경로·ops_alias_2 seam·
// profile_v0110_test 호환).
const ExposureKey = wire.ExposureKey

var ExposureProfile = wire.ExposureProfile

func init() { Register("profile", runProfile) }

// runProfile — `flint profile [--apply normal|minimal]`.
// 무인자: 현재 프로파일 + 근거 지표 + 추천. --apply: 전환 확정
// (setup --exposure 재사용 — 기록 경로 단일 소스).
// 무효 --apply 값·unknown flag는 무효 인자 exit 3(setup
// --exposure 무효 값 exit 3과 정합).
func runProfile(ctx context.Context, st Store, args []string, stdin io.Reader, stdout io.Writer) int {
	// 수동 루프를 wire.ParseArgs 사양 테이블로 이관. 위치 값
	// normal|minimal 허용은 그대로(그 외 위치 토큰은 기존대로 unknown
	// flag). --apply·위치 값 병렬 지정 시 args 순서가 우선(기존 인카운터
	// 덮어쓰기 승계 — 병합 워크로 재현).
	pres, perr := wire.ParseArgs(args, wire.ArgSpec{
		Flags: []wire.FlagSpec{
			{Name: "--apply", Value: true},
		},
		Usage:           "usage: flint profile [--apply normal|minimal]",
		AllowPositional: true,
	})
	if perr != nil {
		fmt.Fprintln(stdout, wire.ErrLine(perr))
		return 3
	}
	apply := ""
	// 위치 토큰·플래그 발생을 args 인덱스로 병합(오류·덮어쓰기 순서 보존).
	type step struct {
		idx      int
		flag     string // "" = 위치 토큰
		val      string
		hasValue bool
	}
	steps := make([]step, 0, len(pres.AllOccs())+len(pres.PosToks()))
	for _, o := range pres.AllOccs() {
		steps = append(steps, step{idx: o.Index, flag: o.Name, val: o.Value, hasValue: o.HasValue})
	}
	for _, p := range pres.PosToks() {
		steps = append(steps, step{idx: p.Index, val: p.Value, hasValue: true})
	}
	sort.Slice(steps, func(a, b int) bool { return steps[a].idx < steps[b].idx })
	for _, s := range steps {
		if s.flag == "--apply" {
			if !s.hasValue || (s.val != "normal" && s.val != "minimal") {
				fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf("profile: --apply requires normal|minimal")))
				return 3
			}
			apply = s.val
			continue
		}
		// 위치 허용(--apply 없이 값만) — 그 외 위치 토큰은 unknown flag.
		if s.val != "normal" && s.val != "minimal" {
			fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf("unknown flag: %s — usage: flint profile [--apply normal|minimal]", s.val)))
			return 3
		}
		apply = s.val
	}
	if err := st.Ping(ctx); err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	if apply != "" {
		return runSetupExposure(ctx, st, apply, stdout)
	}
	// 판정: report 효과 산출 재사용(창 14일 고정 — 판정 창 표준).
	// 산출 실패는 근거 없음 판정으로 강등(§13 — 보고 불변).
	var out reportOutput
	_ = collectEffect(ctx, st, 14, &out)
	th := writeThresholds(ctx, st)
	getKV, _ := metaKV(ctx, st)
	current := ExposureProfile(getKV)
	hint := profileHint(th, out.Effect)
	recommend, reason := "", ""
	switch {
	// ops.ProfileHint 산출(영어) 마커로 판정 — 표준 문구
	// "insufficient sample"·"keep normal"·"low recall lead"(lib/ops
	// report.go 단일 소스). low recall lead는 minimal 기본 분기.
	case strings.Contains(hint, "insufficient sample"):
		recommend, reason = "keep", "insufficient sample (observe first — read_events_n < zero_near_min_read)"
	case strings.Contains(hint, "keep normal"):
		recommend, reason = "normal", "recall-led majority (recall_led_pct >= 50)"
	default:
		recommend, reason = "minimal", "recall-led minority (recall_led_pct < 50) — a usage pattern suited to slimmer hot headlines and nudge suppression"
	}
	res := map[string]any{
		"status":    "ok",
		"current":   current,
		"recommend": recommend,
		"reason":    reason,
		"hint":      hint,
		// 근거 지표 — 판정에 쓰인 신호만(전체 지표는 report가 원천).
		"evidence": map[string]int{
			"read_events_n":  out.Effect.ReadEventsN,
			"recall_led_pct": out.Effect.RecallLedPct,
			"fallback_pct":   out.Effect.FallbackPct,
			"pair_pct":       out.Effect.PairPct,
		},
	}
	if (recommend == "normal" || recommend == "minimal") && recommend != current {
		res["next"] = "flint profile --apply " + recommend
	}
	b, err := json.Marshal(res)
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return 1
	}
	fmt.Fprintln(stdout, string(b))
	return 0
}
