// nudge.go — T73 flint nudge: PostCompact가 남긴 미증류 마커를 Stop 훅에서
// 넛지로, SessionStart에서 plain 안내로 상기시킨다. 모든 실패에 조용히 exit 0
// (비간섭). 무한 넛지 방어 = stop_hook_active 1차 + 마커 1회성(--clear로 소비).
// v0.11.0 R3: 노출 프로파일 분기 — normal은 짧은 문구 1줄, minimal은 출력
// 없이 마커 자동 종결(compact가 이미 pending 적립 — 재적립 없음, 룰링).
// v0.40.0 T2: lib/cli에서 이동(동작·출력·exit 불변 — 식별자 대문자화).
package ops

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/synuns/flint/lib/knowledge"
	"github.com/synuns/flint/lib/wire"
)

// NudgePendingLine — v0.11.0 R3 Stop 넛지 additionalContext 짧은 지시문이자
// SessionStart plain 안내 1줄(동일 문구 — v0.34.3 상수 2종 병합).
const NudgePendingLine = "flint: 증류 대기 — flint nudge"

// NudgeReadHint — SessionStart 안내에 병합되는 read 촉진 힌트(v0.9.5).
const NudgeReadHint = "작업 전 flint read <쿼리> — 기존 지식 확인"

// NudgeRecallStartLine — v0.18.1: 회상 부재 마커(recall-hint.json)의
// SessionStart 안내 1줄. done이 오늘 회상 0회·기록 임계 상태를 마커로
// 남기면 다음 세션 시작에 1회 표시 후 자동 소진된다(경고가 자동 배선
// Stop stdout으로만 나가 안 보이던 갭 폐쇄 — R3 후속).
// v0.18.2: 파일 분리(recall-hint.json) — 증류 대기 마커(pending-distill,
// 내구)와 수명 주기가 달라 같은 파일을 쓰면 서로를 덮어썼다.
const NudgeRecallStartLine = "flint: 지난 세션 회상 없이 기록만 — 작업 전 flint read <쿼리>부터"

// NudgeReasonRecall — done 회상 부재 마커의 Reason 값(진단 표시).
const NudgeReasonRecall = "recall"

// ExposureProfileFn — 노출 프로파일 판정 seam. 실구현(cli profile.go —
// T4 wire 이동 예정)은 cli init이 주입하고, cli 테스트의 var 교체는
// ops_alias_2 래퍼가 호출 시점에 읽어 전파한다(composePsFn 관례 승계).
var ExposureProfileFn func(getKV func(string) (string, error)) string

// NudgeProfile — st의 노출 프로파일. st nil·조회 실패·미설정은 normal
// (프로파일 조회 실패는 normal — R3).
func NudgeProfile(ctx context.Context, st wire.Store) string {
	if st == nil || ExposureProfileFn == nil {
		return "normal"
	}
	g := knowledge.MetaGetKV(ctx, st)
	p := ExposureProfileFn(func(k string) (string, error) { return g(k, "") })
	if p == "" {
		return "normal"
	}
	return p
}

// nudgeStdin — Claude Code 훅 stdin 중 nudge가 보는 필드.
type nudgeStdin struct {
	HookEventName  string `json:"hook_event_name"`
	SessionID      string `json:"session_id"`
	StopHookActive bool   `json:"stop_hook_active"`
}

// RunNudge — 넛지 커맨드. 언제나 0 반환(비간섭).
// v0.18.2: 증류 마커(pending-distill·내구)와 회상 마커(recall-hint·1회성)를
// 독립 파일로 병행 소비한다 — Stop은 증류만 넛지(회상 마커를 증류로 오인해
// 엉터리 넛지·--clear로 회상 알림을 지우던 결함 폐쇄), SessionStart는 둘 다
// 안내(회상은 표시 후 자동 소진).
func RunNudge(ctx context.Context, st wire.Store, args []string, stdin io.Reader, stdout io.Writer) int {
	// --clear: 마커 소비 후 무출력 종료(에러도 조용히). 수동 경로 불변(R3).
	for _, a := range args {
		if a == "--clear" {
			_ = ClearMarker()
			return 0
		}
	}

	distill, derr := ReadTypedMarker(MarkerKindDistill)
	recall, rerr := ReadTypedMarker(MarkerKindRecall)
	// v0.18.1 마이그레이션: 구 배포가 pending-distill.json에 recall을
	// 기록한 잔존 — Reason으로 식별해 회상 취급한다. 소진은 기록된
	// 파일(증류 파일)에서 삭제한다.
	legacyRecall := false
	if distill != nil && distill.Reason == NudgeReasonRecall && recall == nil {
		recall, distill, legacyRecall = distill, nil, true
	}
	// 파손(derr·rerr)은 읽힌 마커가 있으면 무시하고 진행한다. v0.20.2:
	// 외곽 동치 조건문 제거(패치 루프 1차 F6 — 어느 경로에서도 추가 동작
	// 없던 불가능 분기).
	_ = derr
	_ = rerr
	if distill == nil && recall == nil {
		return 0 // 둘 다 부재·파손 모두 조용히
	}

	// minimal: 에이전트 노출 0 — 출력 없이 마커 자동 종결만. 종결 실패는
	// 조용히 스킵(마커 유지). stdin 파식보다 앞선다(빈 stdin 자동 종결 포함).
	if NudgeProfile(ctx, st) == "minimal" {
		if recall != nil {
			_ = ClearTypedMarker(MarkerKindRecall)
		}
		if distill != nil {
			_ = ClearMarker()
		}
		return 0
	}

	// stdin 파싱 실패는 조용히 스킵(비간섭).
	var in nudgeStdin
	if err := json.NewDecoder(stdin).Decode(&in); err != nil {
		return 0
	}

	switch in.HookEventName {
	case "Stop":
		// 회상 마커만 있으면 Stop에서 무출력 — 다음 SessionStart가 소비.
		if distill == nil {
			return 0
		}
		// 이미 Stop을 차단한 적 있으면(stop_hook_active) 재넛지하지 않는다.
		if in.StopHookActive {
			return 0
		}
		b, jerr := json.Marshal(map[string]any{
			"hookSpecificOutput": map[string]any{
				"hookEventName":     "Stop",
				"additionalContext": NudgePendingLine,
			},
		})
		if jerr != nil {
			return 0
		}
		fmt.Fprintln(stdout, string(b))
	case "SessionStart":
		// 회상 부재: 전용 문구 1회 표시 후 자동 소진 — 별도 행동(--clear)
		// 없이 다음 세션이 read로 대응하면 끝(1회성 알림 의미론).
		if recall != nil {
			fmt.Fprintln(stdout, NudgeRecallStartLine)
			if legacyRecall {
				_ = ClearMarker() // 레거시: 기록 파일(pending-distill)에서 소진
			} else {
				_ = ClearTypedMarker(MarkerKindRecall)
			}
		}
		if distill != nil {
			// 기존 안내 뒤에 read 힌트를 같은 줄로 병합(plain 1줄 계약 유지).
			// v0.26.0 R5(예산 가시성 — Letta 인밴드 관례): pending 수/상한을
			// 데이터로 동봉(임계 PendingMax 초과 시에만 — 기본 문구 불변).
			line := NudgePendingLine + " · " + NudgeReadHint
			if n, ok := pendingCount(ctx, st); ok {
				if max := knowledge.WriteThresholds(ctx, st).PendingMax; n > max { // 리뷰 ④: 초과 계약(maintain·sessionhook와 정렬)
					line = fmt.Sprintf("flint: 증류 대기 %d건(상한 %d) — flint maintain --dup-suspects · %s", n, max, NudgeReadHint)
				}
			}
			fmt.Fprintln(stdout, line)
		}
	}
	return 0
}

// pendingCount — pending 지식 수(조회 실패·부재는 ok=false — 조용히 §13).
// 조회 자체는 countPending 단일 구현 위임(v0.34.3 3중 쿼리 복제 수렴).
func pendingCount(ctx context.Context, st wire.Store) (int, bool) {
	if st == nil {
		return 0, false
	}
	n, err := knowledge.CountPending(ctx, st)
	if err != nil {
		return 0, false
	}
	return n, true
}
