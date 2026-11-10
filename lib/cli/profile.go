// profile.go — v0.11.0 R1 노출 프로파일: meta exposure_profile 판독.
// 프로파일은 hot·nudge 동작만 분기한다(다른 명령·exit 계약 불변).
// v0.35.0(D① 완결): 프로파일 판정 — 추천에서 전환까지. profile_hint
// (report)가 안내만 하던 것을 실행 가능 회로로 완결한다. 판정 규칙은
// profileHint와 단일 소스(표본 하한 zero_near_min_read·recall_led_pct
// 50 기준 — METRICS §3 문서화). 근거 지표를 함께 노출해 사용자가 판정을
// 검증할 수 있게 한다(관측 투명성 — GSM 원칙).
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/synuns/flint/lib/store"
)

// ExposureKey — 노출 프로파일 meta 키. 값 normal(기본)|minimal.
const ExposureKey = "exposure_profile"

// ExposureProfile — 노출 프로파일 판독 헬퍼. 값 "minimal"일 때만 minimal,
// 미설정·파손값·조회 오류는 전부 normal(기본값 폴백).
func ExposureProfile(getKV func(string) (string, error)) string {
	v, err := getKV(ExposureKey)
	if err != nil || v != "minimal" {
		return "normal"
	}
	return "minimal"
}

func init() { Register("profile", runProfile) }

// runProfile — `flint profile [--apply normal|minimal]`.
// 무인자: 현재 프로파일 + 근거 지표 + 추천. --apply: 전환 확정
// (setup --exposure 재사용 — 기록 경로 단일 소스).
func runProfile(ctx context.Context, st Store, args []string, stdin io.Reader, stdout io.Writer) int {
	apply := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--apply":
			i++
			if i >= len(args) || (args[i] != "normal" && args[i] != "minimal") {
				fmt.Fprintln(stdout, errLine(fmt.Errorf("profile: --apply requires normal|minimal")))
				return 1
			}
			apply = args[i]
		case "normal", "minimal":
			apply = args[i] // 위치 허용(--apply 없이 값만)
		default:
			fmt.Fprintln(stdout, errLine(fmt.Errorf("unknown flag: %s — 사용법: flint profile [--apply normal|minimal]", args[i])))
			return 1
		}
	}
	if err := st.Ping(ctx); err != nil {
		fmt.Fprintln(stdout, errLine(err))
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
	case strings.Contains(hint, "보류"):
		recommend, reason = "유지", "표본 부족(관측 선행 — read_events_n < zero_near_min_read)"
	case strings.Contains(hint, "normal 유지"):
		recommend, reason = "normal", "회상 선도 다수(recall_led_pct >= 50)"
	default:
		recommend, reason = "minimal", "회상 선도 낮음(recall_led_pct < 50) — hot 헤드라인 축소·nudge 억제가 어울리는 사용 패턴"
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
		fmt.Fprintln(stdout, errLine(err))
		return 1
	}
	fmt.Fprintln(stdout, string(b))
	return 0
}
