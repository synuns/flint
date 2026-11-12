// run.go — 계약층 디스패치(v0.38.0 §1, lib/cli root.go에서 이동).
// Store 공장 함수 주입형: cli 래퍼가 cli.newStore seam을 전달한다.
package wire

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/synuns/flint/lib/store"
)

// DSNFromEnv — DSN 해결 진입점. T74부터 ResolveDSN 위임:
// FLINT_DSN > config.json(LoadConfigDSN) > 기본값.
func DSNFromEnv() string {
	return ResolveDSN()
}

// Run은 args로 커맨드를 실행해 stdout에 기록하고 exit code를 반환한다.
func Run(args []string, stdin io.Reader, stdout io.Writer, newStore func(ctx context.Context, dsn string) (Store, error)) int {
	if len(args) == 0 {
		fmt.Fprintln(stdout, UsageLine())
		return 1
	}
	cmd, rest := args[0], args[1:]
	// help도 store-free fast path(코디네이터 룰링): registry 핸들러가
	// 아니라 접속 전에 직접 출력한다. 인자(`help foo`)는 무시(R1).
	if cmd == "help" {
		return RunHelp(stdout)
	}
	run, ok := Lookup(cmd)
	if !ok {
		// v0.28.0(H③): 이름 이스케이프 — 원시 결합은 호출자 제어 문자열로
		// JSON 구조 자체를 파손할 수 있다.
		b, _ := json.Marshal(map[string]string{"error": "unknown command: " + cmd})
		fmt.Fprintln(stdout, string(b))
		return 1
	}
	ctx := context.Background()
	// version·teardown·mcp는 store-free — DB 미기동에도 즉시 응답
	// (§13, T33 룰링 · T40 · T57: mcp는 도구 호출이 cli.Run으로 각자
	// store를 연결한다). nudge는 v0.13.2 N5부터 store 경유 — nudgeProfile가
	// exposure_profile을 조회해 minimal 계약을 지킨다(구형 store-free 시
	// minimal 자동 종결 불가).
	if cmd == "version" || cmd == "teardown" || cmd == "mcp" || cmd == "update" {
		return run(ctx, nil, rest, stdin, stdout)
	}
	// setup도 DB 다운이 주 시나리오(T40)라 store 없이도 응답한다 — 다만
	// v0.20.1: 정상 기동 DB에선 ping·migrate 단계가 실제로 돌아야 한다.
	// 이전 배선(st 고정 nil)은 ping 클로저가 항상 실패해 건전한 DB에서도
	// 매번 degraded로 끝났다(패치 루프 1차 F2). 연결 시도 후 실패 시에만
	// nil로 진행 — setup의 nil 가드는 DB 다운 안내 경로로 유효 유지.
	if cmd == "setup" {
		var st Store
		if s, err := newStore(ctx, DSNFromEnv()); err == nil {
			st = s
		}
		return run(ctx, st, rest, stdin, stdout)
	}
	st, err := newStore(ctx, DSNFromEnv())
	if err != nil {
		// compact는 훅 진입 커맨드 — DB 부재도 조용히 exit 0(§3 비간섭).
		// st nil로 실행하면 runCompact가 무동작 0을 반환한다.
		if cmd == "compact" {
			return run(ctx, nil, rest, stdin, stdout)
		}
		// nudge도 훅 진입 커맨드(N5) — 에러 JSON을 stdout에 흘리지 않고
		// 조용히 MapExit 코드로만 종료한다(서버 다운=2, 훅 quiet 계약).
		if cmd == "nudge" {
			return store.MapExit(err)
		}
		fmt.Fprintln(stdout, ErrLine(err))
		return store.MapExit(err)
	}
	// v0.25.3(미커버 스윕 high): 명령 완료 후 store 종결 — 단발 CLI는
	// 프로세스 종료가 회수했지만 flint mcp(tools/call마다 cli.Run 호출)의
	// 장기 프로세스에서 pgxpool 2개씩 영구 누적되던 것을 닫는다.
	defer st.Close()
	return run(ctx, st, rest, stdin, stdout)
}

// RunHelp — `flint help` 출력. usage JSON 1줄 + 명령 요약 1줄, exit 0.
// help는 인자 없음 — 남는 인자는 무시한다(R1).
func RunHelp(w io.Writer) int {
	fmt.Fprintln(w, UsageLine())
	fmt.Fprintln(w, HelpSummary())
	return 0
}

// HelpSummary — 명령별 1줄 요약(v0.10.2 R1).
func HelpSummary() string {
	return "write 기록 · read 검색 · hot 주입 · brief 외부 AI 브리프 · status 상태 · doctor 진단 · report 관측 · backup/restore 백업 · maintain/supersede · approve 승격 · done/compact/nudge 훅 · setup/teardown 설치 · audit/diff 시간여행 · mcp 서버 · version"
}

// ErrLine — 에러를 한 줄 JSON으로. 서버 다운은 조용히(server_down) 한 줄만.
func ErrLine(err error) string {
	m := map[string]string{"error": "internal", "message": err.Error()}
	var se *store.SchemaError
	switch {
	case errors.As(err, &se):
		m = map[string]string{"error": "schema", "field": se.Field, "expect": se.Expect}
	case errors.Is(err, store.ErrServerDown):
		m = map[string]string{"error": "server_down"}
	case errors.Is(err, store.ErrRetriesExhausted):
		m = map[string]string{"error": "retries_exhausted"}
	case errors.Is(err, store.ErrDuplicate):
		m = map[string]string{"error": "duplicate"}
	}
	b, _ := json.Marshal(m)
	return string(b)
}
