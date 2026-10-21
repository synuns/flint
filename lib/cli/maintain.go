// maintain 커맨드 — 에이전트 큐레이션 세션 진입점(spec §8).
// `flint maintain --check`(임계값 보고만) · `--invalidate --id N --reason`
// · `--merge --from N --to M`. 판정은 사람(에이전트)이 하고 CLI는 갱신 경로만 연결한다.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"

	"flint/lib/db"
	"flint/lib/store"
)

// MaintStore — maintain이 필요한 저장소 연산(Store + admin 갱신·집계).
type MaintStore interface {
	Store
	Counts(ctx context.Context) (db.Counts, error)
	Invalidate(ctx context.Context, id int, reason string) error
	Merge(ctx context.Context, fromID, toID int) error
}

// realStore 위임 — admin.go(T17) 실구현 연결. 메서드 정의만 여기 둔다.
func (s *realStore) Counts(ctx context.Context) (db.Counts, error) {
	return s.repo.Counts(ctx)
}

func (s *realStore) Invalidate(ctx context.Context, id int, reason string) error {
	return s.repo.Invalidate(ctx, id, reason)
}

func (s *realStore) Merge(ctx context.Context, fromID, toID int) error {
	return s.repo.Merge(ctx, fromID, toID)
}

func init() { Register("maintain", runMaintain) }

// runMaintain — 서브옵션 파싱 후 모드별 실행. --check는 보고만(exit 0),
// 갱신 계열은 성공 0 · 대상 부재(ErrNotFound) 1 · 그 외 MapExit.
func runMaintain(ctx context.Context, st Store, args []string, stdin io.Reader, stdout io.Writer) int {
	ms, ok := st.(MaintStore)
	if !ok {
		fmt.Fprintln(stdout, `{"error":"maintain: unsupported store"}`)
		return 1
	}

	mode := ""
	id, from, to := 0, 0, 0
	reason := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--check", "--invalidate", "--merge":
			mode = args[i]
		case "--id", "--from", "--to":
			i++
			if i >= len(args) {
				fmt.Fprintln(stdout, errLine(fmt.Errorf("maintain: %s requires a number", args[i-1])))
				return 1
			}
			n, err := strconv.Atoi(args[i])
			if err != nil || n <= 0 {
				fmt.Fprintln(stdout, errLine(fmt.Errorf("maintain: invalid number %q", args[i])))
				return 1
			}
			switch args[i-1] {
			case "--id":
				id = n
			case "--from":
				from = n
			case "--to":
				to = n
			}
		case "--reason":
			i++
			if i >= len(args) || args[i] == "" {
				fmt.Fprintln(stdout, errLine(fmt.Errorf("maintain: --reason requires a value")))
				return 1
			}
			reason = args[i]
		default:
			fmt.Fprintln(stdout, errLine(fmt.Errorf("unknown flag: %s", args[i])))
			return 1
		}
	}

	switch mode {
	case "--check":
		return maintainCheck(ctx, ms, stdout)
	case "--invalidate":
		if id == 0 || reason == "" {
			fmt.Fprintln(stdout, errLine(errors.New("maintain: --invalidate requires --id and --reason")))
			return 1
		}
		if err := ms.Invalidate(ctx, id, reason); err != nil {
			return adminErrLine(stdout, err)
		}
		fmt.Fprintf(stdout, `{"status":"invalidated","id":%d}`+"\n", id)
		return 0
	case "--merge":
		if from == 0 || to == 0 {
			fmt.Fprintln(stdout, errLine(errors.New("maintain: --merge requires --from and --to")))
			return 1
		}
		if err := ms.Merge(ctx, from, to); err != nil {
			return adminErrLine(stdout, err)
		}
		fmt.Fprintf(stdout, `{"status":"merged","from":%d,"to":%d}`+"\n", from, to)
		return 0
	default:
		fmt.Fprintln(stdout, errLine(errors.New("usage: flint maintain --check | --invalidate --id N --reason S | --merge --from N --to M")))
		return 1
	}
}

// maintainCheck — Counts vs Thresholds 비교 보고. 임계값은 meta에서 읽고
// (전역 제약: 하드코딩 금지), 경계는 초과분부터(pending==50은 ok, 51부터 경보).
func maintainCheck(ctx context.Context, ms MaintStore, stdout io.Writer) int {
	counts, err := ms.Counts(ctx)
	if err != nil {
		fmt.Fprintln(stdout, errLine(err))
		return store.MapExit(err)
	}
	th, err := loadThresholds(ctx, ms)
	if err != nil {
		fmt.Fprintln(stdout, errLine(err))
		return store.MapExit(err)
	}
	line := `{"status":"ok"}`
	if counts.Pending > th.PendingMax || counts.DupSuspect > th.DupSuspMax {
		line = fmt.Sprintf(`{"status":"review-needed","pending":%d,"dup_suspect":%d}`,
			counts.Pending, counts.DupSuspect)
	}
	// T55: 마지막 복구 훈련(restore --drill) 경과 보고 — 초과 시에만 필드 추가.
	if extra := drillReport(ctx, ms); extra != "" {
		line = line[:len(line)-1] + extra + "}"
	}
	fmt.Fprintln(stdout, line)
	return 0
}

// drillMaxDaysDefault — last_drill 경과 보고 기본 임계일(메타 drill_max_days 우선).
const drillMaxDaysDefault = 30

// drillReport — last_drill 경과 보고(T55). 30일(메타 drill_max_days) 초과 시
// `,"drill":"stale"`, 유효한 기록 자체가 없으면 `,"drill":"never"`를 반환한다.
// 이내면 빈 문자열(보고 없음). 읽기 실패는 --check 결과를 해치지 않게 생략.
func drillReport(ctx context.Context, st Store) string {
	rows, err := queryOnce(ctx, st,
		`SELECT key, value FROM meta WHERE key IN ('last_drill','drill_max_days')`, nil)
	if err != nil {
		return ""
	}
	kv := map[string]string{}
	for _, r := range rows {
		if len(r) == 2 {
			kv[fmt.Sprint(r[0])] = fmt.Sprint(r[1])
		}
	}
	maxDays := drillMaxDaysDefault
	if n, err := strconv.Atoi(kv["drill_max_days"]); err == nil && n > 0 {
		maxDays = n // 무효 값(비수·0·음수)은 기본값 폴백
	}
	last, err := time.Parse(time.RFC3339, kv["last_drill"])
	if err != nil {
		return `,"drill":"never"`
	}
	if time.Since(last) > time.Duration(maxDays)*24*time.Hour {
		return `,"drill":"stale"`
	}
	return ""
}

// loadThresholds — meta 테이블 키-값 → ParseThresholds(미설정 키 기본값).
func loadThresholds(ctx context.Context, st Store) (store.Thresholds, error) {
	rows, err := queryOnce(ctx, st,
		`SELECT key, value FROM meta WHERE key IN ('pending_max','dup_susp_max','conf_floor','stale_sessions')`, nil)
	if err != nil {
		return store.Thresholds{}, err
	}
	kv := make([][2]any, 0, len(rows))
	for _, r := range rows {
		if len(r) == 2 {
			kv = append(kv, [2]any{r[0], r[1]})
		}
	}
	return store.ParseThresholds(kv)
}

// adminErrLine — 갱신 경로 에러 출력. ErrNotFound는 1, 그 외 MapExit.
func adminErrLine(stdout io.Writer, err error) int {
	fmt.Fprintln(stdout, errLine(err))
	if errors.Is(err, db.ErrNotFound) {
		return 1
	}
	return store.MapExit(err)
}
