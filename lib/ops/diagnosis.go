// diagnosis.go — v1.2.0 T5: 규칙 진단 D(spec §3). report가 모은
// effect·counts·daily(ReportOutput)와 thresholds로 규칙 7종(항목 8개 — hot
// 예산 밴드는 하나의 규칙 양방 판정)을 판정해 구조화 항목 배열을 반환한다.
// 순수 함수(LLM 0개 — E 패킷 defer)·정상 침묵 유효(문제 없으면 빈 배열).
//
// 기준 3계층(spec §3): A 자기참조(hot 평균/예산 밴드·hot bytes 추세·hit
// 원가 vs 절약 하한 비교)·B 레지스트리 승계 읽기(zero_near·pending_age는
// doctor 기존 키 승계 — 중복 키 방지, dead는 diag_dead_warn)·C 가치 기본값
// (recall_led '기본 목표' 라벨 — 개인 목표가 아닌 습관 목표 표기).
//
// 사용자 원칙(플랜 T5 확정): 진단은 단일 데이터 모델이다 — text(문장
// 렌더·Sentence)·--json(ReportOutput.Diagnosis 배열)·--html(카드) 세 뷰가
// 같은 ReportOutput.Diagnosis에서 렌더된다(뷰 간 데이터 분열 금지).
package ops

import (
	"fmt"

	"github.com/synuns/flint/lib/store"
)

// DiagnosisItem — 진단 1항목. 문장 계약(spec §3): 신호+수치+기준 출처+
// 행동이 각 한 필드로 구조화되고 Sentence()가 1줄로 합친다. tuning_key는
// 행동으로 이어지는 meta 오버라이드 키(비어 있으면 규칙상 조정 키 없음).
type DiagnosisItem struct {
	Signal    string `json:"signal"`               // 신호 id(kebab-case)
	Value     string `json:"value"`                // 수치 요약(관측값)
	Basis     string `json:"basis"`                // 기준 출처(계층·키·기본값 명시)
	Action    string `json:"action"`               // 권장 행동 1개
	TuningKey string `json:"tuning_key,omitempty"` // 기준 조정 meta 키(없으면 생략)
}

// Sentence — text 뷰 문장 렌더(신호+수치+기준 출처+행동 1줄).
func (d DiagnosisItem) Sentence() string {
	return fmt.Sprintf("%s: %s (기준: %s) → %s", d.Signal, d.Value, d.Basis, d.Action)
}

// Diagnose — 순수 함수 규칙 엔진. 입력은 report 수집 산물(ReportOutput)과
// thresholds·hot_budget(meta hot_budget — knowledge.HotBudget 로드값). 정상
// 침묵: 어느 규칙도 트리거하지 않으면 빈 배열(비-nil — JSON [])을 반환한다.
// 미설정(0) thresholds 키는 DefaultThresholds로 폴백한다(ParseThresholds
// 미설정-0 규약의 소비자측 완결 — v0.16.3 관례 승계).
func Diagnose(out ReportOutput, th store.Thresholds, hotBudget int) []DiagnosisItem {
	th = diagThresholds(th)
	e := out.Effect
	items := make([]DiagnosisItem, 0, 8)

	// ── A계층: 자기참조 기준 ──
	// A1/A2 hot 주입 평균 vs hot_budget 밴드(호출 0·예산 0은 판정하지
	// 않는다 — 제로 분모 관례). 평균은 effect.bytes.hot.avg_bytes(daily
	// 합산과 동일 원천).
	if avg := e.Bytes.Hot.AvgBytes; avg > 0 && hotBudget > 0 {
		ratio := float64(avg) / float64(hotBudget)
		pct := int(ratio * 100)
		if ratio >= th.DiagHotBudgetHigh {
			items = append(items, DiagnosisItem{
				Signal:    "hot_budget_saturation",
				Value:     fmt.Sprintf("hot 주입 평균 %d바이트 = 예산 %d바이트의 %d%%", avg, hotBudget, pct),
				Basis:     fmt.Sprintf("diag_hot_budget_high=%g(hot_budget 대비 주입 평균 비율)", th.DiagHotBudgetHigh),
				Action:    "만성 절단 의심 — hot_budget 상향 검토(요약이 상한에 계속 걸리는 상태)",
				TuningKey: "hot_budget",
			})
		} else if ratio <= th.DiagHotBudgetLow {
			items = append(items, DiagnosisItem{
				Signal:    "hot_budget_slack",
				Value:     fmt.Sprintf("hot 주입 평균 %d바이트 = 예산 %d바이트의 %d%%", avg, hotBudget, pct),
				Basis:     fmt.Sprintf("diag_hot_budget_low=%g(hot_budget 대비 주입 평균 비율)", th.DiagHotBudgetLow),
				Action:    "정보성 — 예산 여유 큼(하향 검토로 주입 절약 여지)",
				TuningKey: "hot_budget",
			})
		}
	}

	// A3 hot bytes 추세 — 창 전반 vs 후반 일별 hot_bytes 합 비교. 전반
	// 합 0(신규 사용 등)은 비교 없음. |증감| ≥ diag_trend_pct%면 급변.
	if cur, prev, ok := hotBytesHalves(out.Days); ok && prev > 0 {
		delta := (cur - prev) * 100 / prev
		if pct := absInt(delta); pct >= th.DiagTrendPct {
			sign := "+"
			if delta < 0 {
				sign = ""
			}
			items = append(items, DiagnosisItem{
				Signal:    "hot_bytes_trend",
				Value:     fmt.Sprintf("hot 주입 바이트 후반 %d vs 전반 %d(%s%d%%)", cur, prev, sign, delta),
				Basis:     fmt.Sprintf("diag_trend_pct=%d%%(창 전후반 일별 hot_bytes 합 비교)", th.DiagTrendPct),
				Action:    "주입 규모 급변 — hot 구성·프로비저닝 변화 원인 확인",
				TuningKey: "diag_trend_pct",
			})
		}
	}

	// A4 주입 원가 vs 절약 하한 — cost.hit_injection_cost_bytes가
	// savings_lower_bound_bytes를 초과하면 보고(절약은 '하한 추정'이라
	// 상회가 곧 순손실 증명은 아님 — 라벨 문구 계약, spec §2.3). 절약
	// 하한 0은 비교 원천이 없어 판정하지 않는다.
	if s, c := e.Cost.SavingsLowerBoundBytes, e.Cost.HitInjectionCostBytes; s > 0 && c > s {
		items = append(items, DiagnosisItem{
			Signal: "hit_cost_exceeds_savings",
			Value:  fmt.Sprintf("주입 원가 %d바이트 > 절약 하한 %d바이트", c, s),
			Basis:  "cost.hit_injection_cost_bytes vs cost.savings_lower_bound_bytes(절약은 하한 추정 — 상회가 순손실 증명은 아님)",
			Action: "회상 반환 크기·노출 수 점검(주입 원가 절감 여지)",
		})
	}

	// ── B계층: 레지스트리(doctor) 승계 읽기 ──
	// B1 zero_near — doctor zero-near 기준 승계(표본 하한 zero_near_min_read
	// ·경고 비율 zero_near_warn_pct — 신규 diag 키를 만들지 않는다).
	if e.ReadEventsN >= th.ZeroNearMinRead && out.ZeroNearPct >= th.ZeroNearWarnPct {
		items = append(items, DiagnosisItem{
			Signal:    "zero_near_high",
			Value:     fmt.Sprintf("zero_near_pct %d%%(read %d건 중 근접)", out.ZeroNearPct, e.ReadEventsN),
			Basis:     fmt.Sprintf("doctor zero-near 승계 zero_near_warn_pct=%d%%·표본 하한 zero_near_min_read=%d", th.ZeroNearWarnPct, th.ZeroNearMinRead),
			Action:    "어휘 불일치 의심 — 검색 품질 개선 여지(제안 스캔·태그 어휘 점검)",
			TuningKey: "zero_near_warn_pct",
		})
	}

	// B2 dead — dead_pct(report effect G3) ≥ diag_dead_warn.
	if e.DeadPct >= th.DiagDeadWarn {
		items = append(items, DiagnosisItem{
			Signal:    "dead_knowledge",
			Value:     fmt.Sprintf("dead_pct %d%%(활성 중 회상 0·장기 경과)", e.DeadPct),
			Basis:     fmt.Sprintf("diag_dead_warn=%d%%(dead_days=%d일 기준 dead_pct)", th.DiagDeadWarn, th.DeadDays),
			Action:    "maintain --stale·--prune-invalidated 정리 검토",
			TuningKey: "diag_dead_warn",
		})
	}

	// B3 pending 적체 — doctor pending-age 기준일 승계(최고령 경과는
	// report 원천에 없어 건수로 보고 — 기준일은 문장에 명시).
	if out.Counts.Pending > 0 {
		items = append(items, DiagnosisItem{
			Signal:    "pending_backlog",
			Value:     fmt.Sprintf("승인 대기 pending %d건", out.Counts.Pending),
			Basis:     fmt.Sprintf("doctor pending-age 승계 pending_age_days=%d일(경과 기준일)", th.PendingAgeDays),
			Action:    "approve 검수·maintain --expire-pending 정리",
			TuningKey: "pending_age_days",
		})
	}

	// ── C계층: 가치 기본값 ──
	// recall_led < diag_recall_led_goal — '기본 목표' 라벨(가치 기본값 —
	// 개인 목표 아님). 표본 하한은 zero_near 관례 승계(관측 없는 판정 금지).
	if e.ReadEventsN >= th.ZeroNearMinRead && e.RecallLedPct < th.DiagRecallLedGoal {
		items = append(items, DiagnosisItem{
			Signal:    "recall_led_below_goal",
			Value:     fmt.Sprintf("회상 선도 세션 %d%%", e.RecallLedPct),
			Basis:     fmt.Sprintf("기본 목표 diag_recall_led_goal=%d%%(가치 기본값 — 습관 목표, 개인 목표 아님)", th.DiagRecallLedGoal),
			Action:    "회상 우선 세션 습관 유도(nudge recall 안내 점검)",
			TuningKey: "diag_recall_led_goal",
		})
	}
	return items
}

// diagThresholds — 미설정(0·무효) 진단 키의 기본값 폴백(ParseThresholds
// 미설정-0 규약의 소비자측 완결). B계층 승계 키(zero_near·pending_age)도
// 여기서 채운다(직접 호출자가 폴백 전 값을 넘긴 경우 방어).
func diagThresholds(th store.Thresholds) store.Thresholds {
	def := store.DefaultThresholds()
	if th.DiagHotBudgetHigh <= 0 {
		th.DiagHotBudgetHigh = def.DiagHotBudgetHigh
	}
	if th.DiagHotBudgetLow <= 0 {
		th.DiagHotBudgetLow = def.DiagHotBudgetLow
	}
	if th.DiagTrendPct <= 0 {
		th.DiagTrendPct = def.DiagTrendPct
	}
	if th.DiagRecallLedGoal <= 0 {
		th.DiagRecallLedGoal = def.DiagRecallLedGoal
	}
	if th.DiagDeadWarn <= 0 {
		th.DiagDeadWarn = def.DiagDeadWarn
	}
	if th.DeadDays <= 0 {
		th.DeadDays = def.DeadDays
	}
	if th.ZeroNearMinRead <= 0 {
		th.ZeroNearMinRead = def.ZeroNearMinRead
	}
	if th.ZeroNearWarnPct <= 0 {
		th.ZeroNearWarnPct = def.ZeroNearWarnPct
	}
	if th.PendingAgeDays <= 0 {
		th.PendingAgeDays = def.PendingAgeDays
	}
	return th
}

// hotBytesHalves — days 창의 전반/후반 hot_bytes 합. 창 1일 이하·전반
// 비었으면 ok=false(추세 비교 불성립).
func hotBytesHalves(days []ReportDay) (cur, prev int, ok bool) {
	if len(days) < 2 {
		return 0, 0, false
	}
	half := len(days) / 2
	for _, d := range days[:half] {
		prev += d.HotBytes
	}
	for _, d := range days[half:] {
		cur += d.HotBytes
	}
	return cur, prev, true
}

// absInt — 절댓값(추세 ±판정용).
func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
