// nudge.go — T73 flint nudge: PostCompact가 남긴 미증류 마커를 Stop 훅에서
// 넛지로, SessionStart에서 plain 안내로 상기시킨다. 모든 실패에 조용히 exit 0
// (비간섭). 무한 넛지 방어 = stop_hook_active 1차 + 마커 1회성(--clear로 소비).
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
)

// nudgeStopDirective — Stop 넛지 additionalContext 지시문(플랜 T73 문구 고정).
const nudgeStopDirective = "flint: 미증류 컴팩트 요약이 있다. 세션에서 지속 지식을 추출해 " +
	"flint write --source hook(status=pending)로 적립하고 flint nudge --clear 로 마커를 지워라."

// nudgeStartLine — SessionStart plain 안내 1줄.
const nudgeStartLine = "flint: 미증류 마커 존재 — 증류 후 flint nudge --clear"

// nudgeStdin — Claude Code 훅 stdin 중 nudge가 보는 필드.
type nudgeStdin struct {
	HookEventName  string `json:"hook_event_name"`
	SessionID      string `json:"session_id"`
	StopHookActive bool   `json:"stop_hook_active"`
}

// runNudge — 넛지 커맨드. st는 미참조(저장소 불필요). 언제나 0 반환.
func runNudge(ctx context.Context, st Store, args []string, stdin io.Reader, stdout io.Writer) int {
	// --clear: 마커 소비 후 무출력 종료(에러도 조용히).
	for _, a := range args {
		if a == "--clear" {
			_ = ClearMarker()
			return 0
		}
	}

	// stdin 파싱 실패는 조용히 스킵(비간섭).
	var in nudgeStdin
	if err := json.NewDecoder(stdin).Decode(&in); err != nil {
		return 0
	}

	m, err := ReadMarker()
	if err != nil || m == nil {
		return 0 // 부재·파손 모두 조용히
	}

	switch in.HookEventName {
	case "Stop":
		// 이미 Stop을 차단한 적 있으면(stop_hook_active) 재넛지하지 않는다.
		if in.StopHookActive {
			return 0
		}
		b, jerr := json.Marshal(map[string]any{
			"hookSpecificOutput": map[string]any{
				"hookEventName":     "Stop",
				"additionalContext": nudgeStopDirective,
			},
		})
		if jerr != nil {
			return 0
		}
		fmt.Fprintln(stdout, string(b))
	case "SessionStart":
		fmt.Fprintln(stdout, nudgeStartLine)
	}
	return 0
}

func init() {
	Register("nudge", runNudge)
}
