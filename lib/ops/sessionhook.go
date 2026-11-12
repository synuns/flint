// sessionhook.go — v0.11.0 R4: SessionStart·Stop 훅 실행을 각 1커맨드로
// 통합(session-start·session-end). 내부에서 기존 hot→nudge / done→nudge
// 시퀀스를 같은 stdout에 순서대로 실행해 출력·계측·exit 승계. 기존 직접
// 실행 커맨드(hot·done·nudge)도 그대로 유효(하위호환).
// v0.20.0(D): SessionStart에서 세션 시작 경계를 session_starts에 기록 —
// 효과 계측의 세션 구간 배정 원천(출력 없는 내부 계정, §13).
// v0.40.0 T2: lib/cli에서 이동(동작·출력·exit 불변 — 식별자 대문자화).
package ops

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	"github.com/synuns/flint/lib/knowledge"
	"github.com/synuns/flint/lib/wire"
)

// SessionStartRecorder — 세션 시작 경계 기록 접합면(v0.20.0). realStore는
// db.Repo.RecordSessionStart로 구현(cli realstore_care.go), 미구현 Store
// (fake)는 조용히 건너뛴다.
type SessionStartRecorder interface {
	RecordSessionStart(ctx context.Context, session string) error
}

// RunSessionStart — SessionStart 훅 1회: 세션 경계 기록 뒤 hot 주입·nudge.
// stdin은 hot·done이 소비하지 않으므로 nudge가 그대로 파싱한다(훅 stdin
// JSON의 hook_event_name으로 nudge 분기). nudge는 언제나 0이라 exit은
// 실질적으로 hot의 코드. 경계 기록 실패는 조용히(§13 계측 원칙 —
// 세션 지표만 0 강등, 주입·안내는 불변).
func RunSessionStart(ctx context.Context, st wire.Store, args []string, stdin io.Reader, stdout io.Writer) int {
	if rec, ok := st.(SessionStartRecorder); ok {
		_ = rec.RecordSessionStart(ctx, SessionLabel(args))
	}
	// v0.20.5(B②): 현재 세션 라벨을 config 경로에 남겨 read가 자동
	// 채택하게 한다 — G4 라벨 자동화. 실패는 조용히(§13 — 라벨은 보조).
	_ = WriteSessionLabel(SessionLabel(args))
	code := knowledge.RunHot(ctx, st, args, stdin, stdout)
	if ncode := RunNudge(ctx, st, nil, stdin, stdout); ncode != 0 {
		return ncode
	}
	// v0.22.0(A·R2): 갱신 안내 — [flint] 경계 내 1줄, 캐시 TTL로 하루
	// 1회 확인. 네트워크 실패·최신 아님은 무출력(§13). 실구현은 cli
	// update.go — MaybeUpdateNoticeFn seam 주입.
	if MaybeUpdateNoticeFn != nil {
		if line := MaybeUpdateNoticeFn(); line != "" {
			fmt.Fprintln(stdout, line)
		}
	}
	// v0.24.0 R2b: 정비 경고 요약(24h 캐시 — 임계값 감사 ③-1 창구 정당화).
	// 발동 창구가 수동 커맨드뿐이던 pending_max를 하루 1회 [flint] 경계
	// 내 1줄로 연결한다. 실패는 무출력(§13).
	if line := maybeMaintenanceNotice(ctx, st); line != "" {
		fmt.Fprintln(stdout, line)
	}
	return code
}

// MaybeUpdateNoticeFn — 갱신 안내 seam(cli update.go 실구현을 cli init이
// 주입 — ops 단독 실행·테스트에서는 무안내).
var MaybeUpdateNoticeFn func() string

// UpdateNoticeTTL — 갱신·정비 안내 캐시 TTL(cli update.go updateNoticeTTL과
// 동일 값 — cli init이 주입해 단일 소스 유지).
var UpdateNoticeTTL = 24 * time.Hour

// maintenanceNoticeCache — 정비 요약 캐시 파일 포맷. checked_at 필드는
// cli update.go updateCache와 동일 계약(세션 훅 소유 캐시 파일).
type maintenanceNoticeCache struct {
	CheckedAt string `json:"checked_at"`
}

// maintenanceCacheFile — 정비 요약 캐시 경로(갱신 캐시 패턴 승계).
func maintenanceCacheFile() string {
	return DataFile("maintenance-check.json")
}

// maybeMaintenanceNotice — pending_max 초과만 검사(가장 발동 빈번한
// 정비 경고 — 24h 캐시로 왕복 1회). 최소 왕복 원칙(§2) 유지.
func maybeMaintenanceNotice(ctx context.Context, st wire.Store) string {
	path := maintenanceCacheFile()
	if path == "" {
		return ""
	}
	if b, err := os.ReadFile(path); err == nil {
		var c maintenanceNoticeCache
		if json.Unmarshal(b, &c) == nil {
			if checked, err := time.Parse(time.RFC3339, c.CheckedAt); err == nil &&
				time.Since(checked) < UpdateNoticeTTL {
				return "" // 오늘 확인 완료 — 무출력
			}
		}
	}
	th := knowledge.WriteThresholds(ctx, st)
	// pending 수 조회는 countPending 단일 구현 위임(v0.34.3 3중 쿼리 복제 수렴).
	n, err := knowledge.CountPending(ctx, st)
	if err != nil {
		return "" // 검사 실패 — 캐시 갱신 안 함(갱신 안내 패턴: 다음 세션 재시도)
	}
	_ = os.WriteFile(path, []byte(`{"checked_at":"`+time.Now().UTC().Format(time.RFC3339)+`"}`), 0o600)
	if n > th.PendingMax {
		return "[flint] pending " + strconv.Itoa(n) + "건(상한 " + strconv.Itoa(th.PendingMax) + ") — flint doctor·maintain 확인"
	}
	return ""
}

// SessionLabel — 인자에서 --session 값을 추출(hot·session-start 공유
// 단일 파서 — 미러 복제로 두 경로가 어긋나는 일을 방지한다, 리뷰 #7).
// knowledge.SessionLabelFn seam이 이 내보내기(별칭 경유)를 주입받는다.
func SessionLabel(args []string) string {
	s := ""
	for i := 0; i < len(args); i++ {
		if args[i] == "--session" && i+1 < len(args) {
			s = args[i+1]
			i++
		}
	}
	return s
}

// RunSessionEnd — Stop 훅 1회: done 판정 뒤 nudge(Stop 추가 컨텍스트).
// done은 언제나 0을 반환하므로 exit은 사실상 0(비간섭 §13 승계).
// 세션 구간은 session_starts만으로 정의([start_i, start_{i+1})) —
// 종료 경계는 별도 기록하지 않는다(룰링, v0.20.0).
func RunSessionEnd(ctx context.Context, st wire.Store, args []string, stdin io.Reader, stdout io.Writer) int {
	// v0.20.5(B②): 세션 종료 — 현재 라벨 정리(종료 후 스테일 방지).
	ClearSessionLabel()
	code := knowledge.RunDone(ctx, st, args, stdin, stdout)
	if ncode := RunNudge(ctx, st, nil, stdin, stdout); ncode != 0 {
		return ncode
	}
	return code
}
