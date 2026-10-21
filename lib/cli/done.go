// done.go — flint done: Stop 훅 종료 경로. 미기록 연속 경고만 내고
// 종료를 절대 막지 않는다(PLAN §13 비간섭 5원칙 — 어떤 경로든 exit 0).
package cli

import (
	"context"
	"fmt"
	"io"
	"strconv"

	"flint/lib/store"
)

// staleSessionStore — T17 StaleSessionRuns 접근. Store 인터페이스는
// v0.1 소유라 확장 대신 타입 단언으로 소비한다(root.go 수정 금지).
type staleSessionStore interface {
	StaleSessionRuns(ctx context.Context, agent string) (int, error)
}

// realStore 위임 메서드 — 같은 패키지 파일이라 여기서 추가만 한다.
func (s *realStore) StaleSessionRuns(ctx context.Context, agent string) (int, error) {
	return s.repo.StaleSessionRuns(ctx, agent)
}

// doneThresholds — loadThresholds(maintain.go 소유) 재사용. 읽기 실패 시
// 기본값 폴백(비간섭: 경고 경로가 본말을 바꾸지 않는다).
func doneThresholds(ctx context.Context, st Store) store.Thresholds {
	th, err := loadThresholds(ctx, st)
	if err != nil {
		return store.DefaultThresholds()
	}
	return th
}

// agentForSession — session 값으로 집계 대상 agent를 찾는다. 숫자면
// sessions.id 조회 결과를 쓰고, 아니거나 미발견이면 값을 그대로 agent로
// 쓴다(훅이 넘기는 세션 식별자 형식이 섞여 있어 둘 다 수용).
func agentForSession(ctx context.Context, st Store, session string) string {
	if id, err := strconv.Atoi(session); err == nil {
		rows, err := queryOnce(ctx, st, `SELECT agent FROM sessions WHERE id = $1`, []any{id})
		if err == nil && len(rows) > 0 {
			if a, ok := rows[0][0].(string); ok && a != "" {
				return a
			}
		}
	}
	return session
}

// staleRuns — Store가 StaleSessionRuns를 미구현(레거시 fake)이면
// 경고 없는 0이 안전한 기본값이다.
func staleRuns(ctx context.Context, st Store, agent string) (int, error) {
	if ss, ok := st.(staleSessionStore); ok {
		return ss.StaleSessionRuns(ctx, agent)
	}
	return 0, nil
}

// runDone — `flint done --session <id>`: 미기록 연속 >= StaleSessions면
// stale 경고, 그 외 ok. 경고일 뿐 종료를 막지 않는다(항상 exit 0).
func runDone(ctx context.Context, st Store, args []string, stdin io.Reader, stdout io.Writer) int {
	session := ""
	for i := 0; i < len(args); i++ {
		if args[i] == "--session" && i+1 < len(args) {
			session = args[i+1]
			i++
		}
	}
	if session == "" {
		// 사용법 오류도 종료를 막지 않는다 — 한 줄 JSON, exit 0(§13).
		fmt.Fprintln(stdout, `{"error":"usage: flint done --session <id>"}`)
		return 0
	}
	th := doneThresholds(ctx, st)
	runs, err := staleRuns(ctx, st, agentForSession(ctx, st, session))
	if err != nil {
		fmt.Fprintln(stdout, errLine(err))
		return 0
	}
	if runs >= th.StaleSessions {
		fmt.Fprintf(stdout, `{"status":"stale","runs":%d,"warning":"연속 %d세션 미기록"}`+"\n", runs, runs)
		return 0
	}
	fmt.Fprintln(stdout, `{"status":"ok"}`)
	return 0
}

func init() {
	Register("done", runDone)
}
