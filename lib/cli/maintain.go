// maintain 커맨드 — 에이전트 큐레이션 세션 진입점(spec §8).
// `flint maintain --check`(임계값 보고만) · `--invalidate --id N --reason`
// · `--merge --from N --to M`. 판정은 사람(에이전트)이 하고 CLI는 갱신 경로만 연결한다.
package cli

import (
	"context"
	"encoding/json"
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
	days, limit := 0, 20 // --stale: days=0 → meta stale_days(기본 90) 폴백
	reason := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--check", "--invalidate", "--merge", "--stale":
			mode = args[i]
		case "--days", "--limit":
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
			if args[i-1] == "--days" {
				days = n
			} else {
				limit = n
			}
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
	case "--stale":
		return maintainStale(ctx, ms, stdout, days, limit)
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
		fmt.Fprintln(stdout, errLine(errors.New("usage: flint maintain --check | --stale [--days N] [--limit M] | --invalidate --id N --reason S | --merge --from N --to M")))
		return 1
	}
}

// staleItem — maintain --stale 보고 항목. never_read 버킷(read_count=0)을
// 구분 표시한다(Devin "사용 안 한 노트" 준거). 보고 전용 — 갱신 없음.
type staleItem struct {
	ID        int    `json:"id"`
	Topic     string `json:"topic"`
	NeverRead bool   `json:"never_read"`
	StaleDays int    `json:"stale_days"` // 기준 시각(last_read_at·없으면 ingested_at) 경과일
}

// maintainStale — active 중 오래 참조되지 않은 항목 보고(보고 전용·exit 0).
// 대상: (last_read_at < now-days) OR (read_count=0 AND ingested_at < now-days).
// 경계는 미만 — 컷오프와 동일 시각은 포함하지 않는다. cron 친화적.
func maintainStale(ctx context.Context, ms MaintStore, stdout io.Writer, daysOverride, limit int) int {
	th, err := loadThresholds(ctx, ms)
	if err != nil {
		fmt.Fprintln(stdout, errLine(err))
		return store.MapExit(err)
	}
	days := th.StaleDays
	if daysOverride > 0 {
		days = daysOverride // --days N이 meta stale_days보다 우선
	}
	// T70: 컷오프를 Go에서 계산해 timestamptz 파라미터로 전달한다. SQL의
	// ($1 * interval) 곱은 Doltgres가 $1을 interval(OID 1186)로 추론해 pgx가
	// int 인코딩에 실패한다(maintain --stale exit 1). SQL·Go 필터가 같은
	// 컷오프를 쓰므로 경계도 일치한다.
	cutoff := time.Now().Add(-time.Duration(days) * 24 * time.Hour)
	rows, err := queryOnce(ctx, ms, `
		SELECT id, topic, last_read_at, ingested_at, read_count
		FROM knowledge
		WHERE status = 'active' AND invalid_at IS NULL
		  AND ((last_read_at IS NOT NULL AND last_read_at < $1)
		   OR (read_count = 0 AND ingested_at < $1))
		ORDER BY COALESCE(last_read_at, ingested_at) ASC
		LIMIT $2`, []any{cutoff, limit})
	if err != nil {
		fmt.Fprintln(stdout, errLine(err))
		return store.MapExit(err)
	}
	items := []staleItem{}
	for _, r := range rows {
		if len(r) < 5 {
			continue
		}
		id, ok := anyToInt(r[0])
		if !ok {
			continue
		}
		topic, _ := r[1].(string)
		readCount, _ := anyToInt(r[4])
		ref, ok := anyToTime(r[2]) // last_read_at 우선, 없으면 ingested_at
		if !ok || ref.IsZero() {
			ref, ok = anyToTime(r[3])
		}
		if !ok || !ref.Before(cutoff) { // 경계 미만 — 컷오프 이후(활성) 제외
			continue
		}
		items = append(items, staleItem{
			ID:        id,
			Topic:     topic,
			NeverRead: readCount == 0,
			StaleDays: int(time.Since(ref).Hours() / 24),
		})
	}
	out, mErr := json.Marshal(struct {
		Status string      `json:"status"`
		Days   int         `json:"days"`
		Count  int         `json:"count"`
		Items  []staleItem `json:"items"`
	}{Status: "ok", Days: days, Count: len(items), Items: items})
	if mErr != nil {
		fmt.Fprintln(stdout, errLine(mErr))
		return 1
	}
	fmt.Fprintln(stdout, string(out))
	return 0
}

// anyToInt — 셀 값을 int로(pgx int32·int64·float64·문자열 수용, fake 테스트 대응).
func anyToInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int32:
		return int(n), true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	case string:
		i, err := strconv.Atoi(n)
		return i, err == nil
	default:
		return 0, false
	}
}

// anyToTime — 셀 값을 시각으로(time.Time·RFC3339 문자열 수용).
func anyToTime(v any) (time.Time, bool) {
	switch t := v.(type) {
	case time.Time:
		return t, true
	case string:
		if t == "" {
			return time.Time{}, false
		}
		ts, err := time.Parse(time.RFC3339, t)
		return ts, err == nil
	default:
		return time.Time{}, false
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
	// T66: stall 경고 — last_hot_at·last_write_at 경과 > grace(warnings 배열).
	if warns := stallWarnings(ctx, ms, th); len(warns) > 0 {
		b, mErr := json.Marshal(warns)
		if mErr == nil {
			line = line[:len(line)-1] + `,"warnings":` + string(b) + `}`
		}
	}
	// T55: 마지막 복구 훈련(restore --drill) 경과 보고 — 초과 시에만 필드 추가.
	if extra := drillReport(ctx, ms); extra != "" {
		line = line[:len(line)-1] + extra + "}"
	}
	fmt.Fprintln(stdout, line)
	return 0
}

// stallWarnings — last-seen heartbeat 경과 진단(spec §1: nodata 패턴).
// last_hot_at 경과 > stall_hot_h(기본 48h)·last_write_at 경과 > stall_write_h
// (기본 72h) 초과 시 경고 문자열. warn은 exit 0 유지(brew doctor 준용).
func stallWarnings(ctx context.Context, st Store, th store.Thresholds) []string {
	rows, err := queryOnce(ctx, st,
		`SELECT key, value FROM meta WHERE key IN ('last_hot_at','last_write_at')`, nil)
	if err != nil {
		return nil // 읽기 실패는 --check 결과를 해치지 않게 생략
	}
	kv := map[string]string{}
	for _, r := range rows {
		if len(r) == 2 {
			kv[fmt.Sprint(r[0])] = fmt.Sprint(r[1])
		}
	}
	var warns []string
	now := time.Now()
	if ts, ok := anyToTime(kv["last_hot_at"]); ok {
		if el := now.Sub(ts); el > time.Duration(th.StallHotH)*time.Hour {
			warns = append(warns, fmt.Sprintf(
				"hot stall: %dh since last_hot_at (grace %dh)", int(el.Hours()), th.StallHotH))
		}
	}
	if ts, ok := anyToTime(kv["last_write_at"]); ok {
		if el := now.Sub(ts); el > time.Duration(th.StallWriteH)*time.Hour {
			warns = append(warns, fmt.Sprintf(
				"write stall: %dh since last_write_at (grace %dh)", int(el.Hours()), th.StallWriteH))
		}
	}
	return warns
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
// v0.6 키(stale_days·stall_hot_h·stall_write_h)는 ParseThresholds가 미설정을
// 0으로 두므로(기존 테스트 호환 규약) DefaultThresholds 값으로 폴백한다.
func loadThresholds(ctx context.Context, st Store) (store.Thresholds, error) {
	rows, err := queryOnce(ctx, st,
		`SELECT key, value FROM meta WHERE key IN ('pending_max','dup_susp_max','conf_floor','stale_sessions','stale_days','stall_hot_h','stall_write_h')`, nil)
	if err != nil {
		return store.Thresholds{}, err
	}
	kv := make([][2]any, 0, len(rows))
	for _, r := range rows {
		if len(r) == 2 {
			kv = append(kv, [2]any{r[0], r[1]})
		}
	}
	th, err := store.ParseThresholds(kv)
	if err != nil {
		return th, err
	}
	def := store.DefaultThresholds()
	if th.StaleDays <= 0 {
		th.StaleDays = def.StaleDays
	}
	if th.StallHotH <= 0 {
		th.StallHotH = def.StallHotH
	}
	if th.StallWriteH <= 0 {
		th.StallWriteH = def.StallWriteH
	}
	return th, nil
}

// adminErrLine — 갱신 경로 에러 출력. ErrNotFound는 1, 그 외 MapExit.
func adminErrLine(stdout io.Writer, err error) int {
	fmt.Fprintln(stdout, errLine(err))
	if errors.Is(err, db.ErrNotFound) {
		return 1
	}
	return store.MapExit(err)
}
