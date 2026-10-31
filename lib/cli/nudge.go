// nudge.go — T73 flint nudge: PostCompact가 남긴 미증류 마커를 Stop 훅에서
// 넛지로, SessionStart에서 plain 안내로 상기시킨다. 모든 실패에 조용히 exit 0
// (비간섭). 무한 넛지 방어 = stop_hook_active 1차 + 마커 1회성(--clear로 소비).
// v0.11.0 R3: 노출 프로파일 분기 — normal은 짧은 문구 1줄, minimal은 출력
// 없이 마커 자동 종결(compact가 이미 pending 적립 — 재적립 없음, 룰링).
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
)

// nudgeStopShort — v0.11.0 R3 Stop 넛지 additionalContext 짧은 지시문(문구 고정).
const nudgeStopShort = "flint: 증류 대기 — flint nudge"

// nudgeStartLine — SessionStart plain 안내 1줄(R3: Stop과 동일 축소).
const nudgeStartLine = "flint: 증류 대기 — flint nudge"

// nudgeReadHint — SessionStart 안내에 병합되는 read 촉진 힌트(v0.9.5).
const nudgeReadHint = "작업 전 flint read <쿼리> — 기존 지식 확인"

// exposureProfileFn — T1 ExposureProfile seam. 기본 실구현, 테스트는
// fakeProfileStore가 페이크로 교체(SUPERLITE_REAL=1은 실구현 통과).
var exposureProfileFn = ExposureProfile

// nudgeProfile — st의 노출 프로파일. st nil·조회 실패·미설정은 normal
// (프로파일 조회 실패는 normal — R3).
func nudgeProfile(ctx context.Context, st Store) string {
	if st == nil {
		return "normal"
	}
	g := metaGetKV(ctx, st)
	p := exposureProfileFn(func(k string) (string, error) { return g(k, "") })
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

// runNudge — 넛지 커맨드. 언제나 0 반환(비간섭).
func runNudge(ctx context.Context, st Store, args []string, stdin io.Reader, stdout io.Writer) int {
	// --clear: 마커 소비 후 무출력 종료(에러도 조용히). 수동 경로 불변(R3).
	for _, a := range args {
		if a == "--clear" {
			_ = ClearMarker()
			return 0
		}
	}

	m, err := ReadMarker()
	if err != nil || m == nil {
		return 0 // 부재·파손 모두 조용히
	}

	// minimal: 에이전트 노출 0 — 출력 없이 마커 자동 종결만. 종결 실패는
	// 조용히 스킵(마커 유지). stdin 파식보다 앞선다(빈 stdin 자동 종결 포함).
	if nudgeProfile(ctx, st) == "minimal" {
		_ = ClearMarker()
		return 0
	}

	// stdin 파싱 실패는 조용히 스킵(비간섭).
	var in nudgeStdin
	if err := json.NewDecoder(stdin).Decode(&in); err != nil {
		return 0
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
				"additionalContext": nudgeStopShort,
			},
		})
		if jerr != nil {
			return 0
		}
		fmt.Fprintln(stdout, string(b))
	case "SessionStart":
		// 기존 안내 뒤에 read 힌트를 같은 줄로 병합(plain 1줄 계약 유지).
		fmt.Fprintln(stdout, nudgeStartLine+" · "+nudgeReadHint)
	}
	return 0
}

func init() {
	Register("nudge", runNudge)
}
