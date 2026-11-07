// sessionhook.go — v0.11.0 R4: SessionStart·Stop 훅 실행을 각 1커맨드로
// 통합(session-start·session-end). 내부에서 기존 hot→nudge / done→nudge
// 시퀀스를 같은 stdout에 순서대로 실행해 출력·계측·exit 승계. 기존 직접
// 실행 커맨드(hot·done·nudge)도 그대로 유효(하위호환).
// v0.20.0(D): SessionStart에서 세션 시작 경계를 session_starts에 기록 —
// 효과 계측의 세션 구간 배정 원천(출력 없는 내부 계정, §13).
package cli

import (
	"context"
	"io"
)

// SessionStartRecorder — 세션 시작 경계 기록 접합면(v0.20.0). realStore는
// db.Repo.RecordSessionStart로 구현, 미구현 Store(fake)는 조용히 건너뛴다.
type SessionStartRecorder interface {
	RecordSessionStart(ctx context.Context, session string) error
}

// realStore 위임 — RecordSessionStart 실구현 연결.
func (s *realStore) RecordSessionStart(ctx context.Context, session string) error {
	return s.repo.RecordSessionStart(ctx, session)
}

// runSessionStart — SessionStart 훅 1회: 세션 경계 기록 뒤 hot 주입·nudge.
// stdin은 hot·done이 소비하지 않으므로 nudge가 그대로 파싱한다(훅 stdin
// JSON의 hook_event_name으로 nudge 분기). nudge는 언제나 0이라 exit은
// 실질적으로 hot의 코드. 경계 기록 실패는 조용히(§13 계측 원칙 —
// 세션 지표만 0 강등, 주입·안내는 불변).
func runSessionStart(ctx context.Context, st Store, args []string, stdin io.Reader, stdout io.Writer) int {
	if rec, ok := st.(SessionStartRecorder); ok {
		_ = rec.RecordSessionStart(ctx, sessionLabel(args))
	}
	// v0.20.5(B②): 현재 세션 라벨을 config 경로에 남겨 read가 자동
	// 채택하게 한다 — G4 라벨 자동화. 실패는 조용히(§13 — 라벨은 보조).
	_ = WriteSessionLabel(sessionLabel(args))
	code := runHot(ctx, st, args, stdin, stdout)
	if ncode := runNudge(ctx, st, nil, stdin, stdout); ncode != 0 {
		return ncode
	}
	return code
}

// sessionLabel — 인자에서 --session 값을 추출(hot·session-start 공유
// 단일 파서 — 미러 복제로 두 경로가 어긋나는 일을 방지한다, 리뷰 #7).
func sessionLabel(args []string) string {
	s := ""
	for i := 0; i < len(args); i++ {
		if args[i] == "--session" && i+1 < len(args) {
			s = args[i+1]
			i++
		}
	}
	return s
}

// runSessionEnd — Stop 훅 1회: done 판정 뒤 nudge(Stop 추가 컨텍스트).
// done은 언제나 0을 반환하므로 exit은 사실상 0(비간섭 §13 승계).
// 세션 구간은 session_starts만으로 정의([start_i, start_{i+1})) —
// 종료 경계는 별도 기록하지 않는다(룰링, v0.20.0).
func runSessionEnd(ctx context.Context, st Store, args []string, stdin io.Reader, stdout io.Writer) int {
	// v0.20.5(B②): 세션 종료 — 현재 라벨 정리(종료 후 스테일 방지).
	ClearSessionLabel()
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
