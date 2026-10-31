// sessionhook.go — v0.11.0 R4: SessionStart·Stop 훅 실행을 각 1커맨드로
// 통합(session-start·session-end). 내부에서 기존 hot→nudge / done→nudge
// 시퀀스를 같은 stdout에 순서대로 실행해 출력·계측·exit 승계. 기존 직접
// 실행 커맨드(hot·done·nudge)도 그대로 유효(하위호환).
package cli

import (
	"context"
	"io"
)

// runSessionStart — SessionStart 훅 1회: hot 주입 뒤 nudge 안내.
// stdin은 hot·done이 소비하지 않으므로 nudge가 그대로 파싱한다(훅 stdin
// JSON의 hook_event_name으로 nudge 분기). nudge는 언제나 0이라 exit은
// 실질적으로 hot의 코드.
func runSessionStart(ctx context.Context, st Store, args []string, stdin io.Reader, stdout io.Writer) int {
	code := runHot(ctx, st, args, stdin, stdout)
	if ncode := runNudge(ctx, st, nil, stdin, stdout); ncode != 0 {
		return ncode
	}
	return code
}

// runSessionEnd — Stop 훅 1회: done 판정 뒤 nudge(Stop 추가 컨텍스트).
// done은 언제나 0을 반환하므로 exit은 사실상 0(비간섭 §13 승계).
func runSessionEnd(ctx context.Context, st Store, args []string, stdin io.Reader, stdout io.Writer) int {
	code := runDone(ctx, st, args, stdin, stdout)
	if ncode := runNudge(ctx, st, nil, stdin, stdout); ncode != 0 {
		return ncode
	}
	return code
}

func init() {
	Register("session-start", runSessionStart)
	Register("session-end", runSessionEnd)
}
