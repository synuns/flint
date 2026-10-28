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
	"strings"
	"time"

	"flint/lib/db"
	"flint/lib/store"
)

// MaintStore — maintain이 필요한 저장소 연산(Store + admin 갱신·집계).
type MaintStore interface {
	Store
	Counts(ctx context.Context) (db.Counts, error)
	DupGroups(ctx context.Context, limit int) ([]db.DupGroup, error)
	Invalidate(ctx context.Context, id int, reason string) error
	Merge(ctx context.Context, fromID, toID int) error
}

// realStore 위임 — admin.go(T17) 실구현 연결. 메서드 정의만 여기 둔다.
func (s *realStore) Counts(ctx context.Context) (db.Counts, error) {
	return s.repo.Counts(ctx)
}

func (s *realStore) DupGroups(ctx context.Context, limit int) ([]db.DupGroup, error) {
	return s.repo.DupGroups(ctx, limit)
}

func (s *realStore) Invalidate(ctx context.Context, id int, reason string) error {
	return s.repo.Invalidate(ctx, id, reason)
}

func (s *realStore) Merge(ctx context.Context, fromID, toID int) error {
	return s.repo.Merge(ctx, fromID, toID)
}

func init() { Register("maintain", runMaintain) }

// maintainValueFlags — 값을 받는 플래그 전체(모드 귀속 검증 대상).
var maintainValueFlags = []string{"--days", "--limit", "--id", "--from", "--to", "--reason",
	"--ids", "--supersede", "--by"}

// maintainModeFlags — 모드별 허용 값 플래그. 무관 플래그는 조용히 무시하지
// 않고 에러로 거부한다(--days·--limit이 --stale 아닌 모드에서 흐르는 등).
var maintainModeFlags = map[string]map[string]bool{
	"--check":            {},
	"--dup-suspects":     {"--limit": true},
	"--stale":            {"--days": true, "--limit": true},
	"--invalidate-stale": {"--days": true, "--limit": true},
	"--invalidate":       {"--id": true, "--reason": true, "--ids": true},
	"--merge":            {"--from": true, "--to": true},
	"--supersede":        {"--supersede": true, "--by": true},
}

// maintainYesModes — --yes(실행 확정)가 뜻 있는 모드. 나머지 모드에서의
// --yes도 조용한 무시 대신 에러로 거부한다.
var maintainYesModes = map[string]bool{"--invalidate": true, "--invalidate-stale": true}

// maintainRetryDelays — 유지보수 쓰기 경로 Retry 간격(전역 제약: 쓰기는
// 40001 대응 Retry 동반). 테스트는 즉시 재시도로 교체.
var maintainRetryDelays = db.DefaultDelays

// retryWrite — 쓰기(UPDATE+Commit) 클로저를 Retry로 감싼다(전역 제약).
func retryWrite(ctx context.Context, fn func() error) error {
	return db.Retry(ctx, fn, maintainRetryDelays)
}

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
	old, by := 0, 0 // --supersede OLD --by NEW
	days, limit := 0, 20 // --stale: days=0 → meta stale_days(기본 90) 폴백
	dupLimit := 10       // --dup-suspects 기본(스펙 R4: --limit M 기본 10)
	reason := ""
	yes := false
	var idList []int // --ids 파싱 결과(스냅샷 — 재평가 없음)
	seen := map[string]bool{}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--check", "--dup-suspects", "--merge", "--stale", "--invalidate", "--invalidate-stale":
			mode = args[i]
		case "--days", "--limit":
			seen[args[i]] = true
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
				limit, dupLimit = n, n
			}
		case "--id", "--from", "--to":
			seen[args[i]] = true
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
			seen[args[i]] = true
			i++
			if i >= len(args) || args[i] == "" {
				fmt.Fprintln(stdout, errLine(fmt.Errorf("maintain: --reason requires a value")))
				return 1
			}
			reason = args[i]
		case "--supersede":
			mode = args[i]
			seen[args[i]] = true
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
			old = n
		case "--by":
			seen[args[i]] = true
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
			by = n
		case "--ids":
			seen[args[i]] = true
			i++
			if i >= len(args) || args[i] == "" {
				fmt.Fprintln(stdout, errLine(fmt.Errorf("maintain: --ids requires a value")))
				return 1
			}
			ids, perr := parseIDList(args[i])
			if perr != nil {
				fmt.Fprintln(stdout, errLine(perr))
				return 1
			}
			idList = ids
		case "--yes":
			yes = true
		default:
			fmt.Fprintln(stdout, errLine(fmt.Errorf("unknown flag: %s", args[i])))
			return 1
		}
	}

	// 모드 귀속 검증: 해당 모드가 받지 않는 값 플래그는 에러(조용한 무시 금지).
	if allowed, ok := maintainModeFlags[mode]; ok {
		for _, f := range maintainValueFlags {
			if seen[f] && !allowed[f] {
				fmt.Fprintln(stdout, errLine(fmt.Errorf("maintain: %s is not valid with %s", f, mode)))
				return 1
			}
		}
	}
	// --yes는 실행 확정 마커 — 해당 없는 모드에서 흐르면 에러(동일 원칙).
	if yes && !maintainYesModes[mode] {
		fmt.Fprintln(stdout, errLine(fmt.Errorf("maintain: --yes is not valid with %s", mode)))
		return 1
	}

	switch mode {
	case "--check":
		return maintainCheck(ctx, ms, stdout)
	case "--dup-suspects":
		return maintainDupSuspects(ctx, ms, stdout, dupLimit)
	case "--stale":
		return maintainStale(ctx, ms, stdout, days, limit)
	case "--invalidate-stale":
		return maintainInvalidateStale(ctx, ms, stdout, days, limit, yes)
	case "--supersede":
		if old == 0 || by == 0 {
			fmt.Fprintln(stdout, errLine(errors.New("maintain: --supersede requires OLD and --by NEW")))
			return 1
		}
		return maintainSupersede(ctx, ms, stdout, old, by)
	case "--invalidate":
		if len(idList) > 0 {
			if !yes { // 스펙 §2: --ids는 --yes 병행 필요(실행 확정)
				fmt.Fprintln(stdout, errLine(errors.New("maintain: --invalidate --ids requires --yes")))
				return 1
			}
			return maintainInvalidateIDs(ctx, ms, stdout, idList)
		}
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
		fmt.Fprintln(stdout, errLine(errors.New("usage: flint maintain --check | --dup-suspects [--limit M] | --stale [--days N] [--limit M] | --invalidate-stale [--yes] [--days N] [--limit M] | --invalidate (--id N --reason S | --ids 1,2,3 --yes) | --merge --from N --to M | --supersede OLD --by NEW")))
		return 1
	}
}

// parseIDList — "1,2,3" 형태 id 스냅샷 파싱(빈 토큰·비양수·비수 거부).
func parseIDList(s string) ([]int, error) {
	parts := strings.Split(s, ",")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		n, err := strconv.Atoi(p)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("maintain: --ids invalid id %q", p)
		}
		out = append(out, n)
	}
	return out, nil
}

// staleItem — maintain --stale 보고 항목. never_read 버킷(read_count=0)을
// 사유(reason)로 구분 표시한다(Devin "사용 안 한 노트" 준거). 보고 전용.
type staleItem struct {
	ID        int    `json:"id"`
	Topic     string `json:"topic"`
	NeverRead bool   `json:"never_read"`
	Reason    string `json:"reason"` // never_read(미참조) | stale(경과)
	StaleDays int    `json:"stale_days"` // 기준 시각(last_read_at·없으면 ingested_at) 경과일
}

// maintainDupSuspects — active 중 동일 topic 2건+ 그룹 보고(보고 전용·
// exit 0). 판정(병합·무효화)은 기존 --merge·--invalidate로 에이전트 수행
// — SQL 자동 판정 금지 원칙 유지(spec R4).
func maintainDupSuspects(ctx context.Context, ms MaintStore, stdout io.Writer, limit int) int {
	groups, err := ms.DupGroups(ctx, limit)
	if err != nil {
		fmt.Fprintln(stdout, errLine(err))
		return store.MapExit(err)
	}
	fmt.Fprintln(stdout, dupSuspectsJSON(groups))
	return 0
}

// dupSuspectsJSON — --dup-suspects 출력 한 줄 JSON. ids null 방지를 위해
// 빈 슬라이스로 정규화한다(nil이면 "ids":null이 되어 형식 계약 위반).
func dupSuspectsJSON(groups []db.DupGroup) string {
	type dupGroupJSON struct {
		Topic string `json:"topic"`
		IDs   []int  `json:"ids"`
	}
	out := struct {
		Status string         `json:"status"`
		Groups []dupGroupJSON `json:"groups"`
	}{Status: "dup-suspects", Groups: make([]dupGroupJSON, 0, len(groups))}
	for _, g := range groups {
		ids := g.IDs
		if ids == nil {
			ids = []int{}
		}
		out.Groups = append(out.Groups, dupGroupJSON{Topic: g.Topic, IDs: ids})
	}
	b, err := json.Marshal(out)
	if err != nil {
		return `{"status":"dup-suspects","groups":[]}`
	}
	return string(b)
}

// maintainStale — active 중 오래 참조되지 않은 항목 보고(보고 전용·exit 0).
// v0.8: 항목별 사유(reason)와 요약(summary "N to invalidate") 추가.
func maintainStale(ctx context.Context, ms MaintStore, stdout io.Writer, daysOverride, limit int) int {
	items, days, err := listStale(ctx, ms, daysOverride, limit)
	if err != nil {
		fmt.Fprintln(stdout, errLine(err))
		return store.MapExit(err)
	}
	out, mErr := staleReport(items, days)
	if mErr != nil {
		fmt.Fprintln(stdout, errLine(mErr))
		return 1
	}
	fmt.Fprintln(stdout, out)
	return 0
}

// listStale — stale 대상 산출(공통: --stale 보고·--invalidate-stale 적용).
// 대상: (last_read_at < now-days) OR (read_count=0 AND ingested_at < now-days).
// 경계는 미만 — 컷오프와 동일 시각은 포함하지 않는다. cron 친화적.
func listStale(ctx context.Context, ms MaintStore, daysOverride, limit int) ([]staleItem, int, error) {
	th, err := loadThresholds(ctx, ms)
	if err != nil {
		return nil, 0, err
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
		return nil, days, err
	}
	items := []staleItem{}
	for _, r := range rows {
		if len(r) < 5 {
			continue
		}
		id, ok := cellToInt(r[0])
		if !ok {
			continue
		}
		topic, _ := r[1].(string)
		readCount, _ := cellToInt(r[4])
		ref, ok := anyToTime(r[2]) // last_read_at 우선, 없으면 ingested_at
		if !ok || ref.IsZero() {
			ref, ok = anyToTime(r[3])
		}
		if !ok || !ref.Before(cutoff) { // 경계 미만 — 컷오프 이후(활성) 제외
			continue
		}
		reason := "stale"
		if readCount == 0 {
			reason = "never_read"
		}
		items = append(items, staleItem{
			ID:        id,
			Topic:     topic,
			NeverRead: readCount == 0,
			Reason:    reason,
			StaleDays: int(time.Since(ref).Hours() / 24),
		})
	}
	return items, days, nil
}

// staleReport — dry-run(기본) 출력 한 줄 JSON. --stale·--invalidate-stale
// dry-run이 같은 모양을 쓴다(스펙 §2).
func staleReport(items []staleItem, days int) (string, error) {
	out, err := json.Marshal(struct {
		Status  string      `json:"status"`
		Days    int         `json:"days"`
		Count   int         `json:"count"`
		Items   []staleItem `json:"items"`
		Summary string      `json:"summary"`
	}{Status: "ok", Days: days, Count: len(items), Items: items,
		Summary: fmt.Sprintf("%d to invalidate", len(items))})
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// maintainInvalidateStale — 배치 무효화(spec §2). dry-run 기본(마커 없으면
// --stale과 동일 출력·갱신 없음). --yes 시 목록 산출 시점 id 전체에
// UPDATE(status='invalidated'·invalid_at=now)+Commit — 쓰기는 Retry 동반.
func maintainInvalidateStale(ctx context.Context, ms MaintStore, stdout io.Writer,
	daysOverride, limit int, apply bool) int {
	items, days, err := listStale(ctx, ms, daysOverride, limit)
	if err != nil {
		fmt.Fprintln(stdout, errLine(err))
		return store.MapExit(err)
	}
	if !apply || len(items) == 0 {
		out, mErr := staleReport(items, days)
		if mErr != nil {
			fmt.Fprintln(stdout, errLine(mErr))
			return 1
		}
		fmt.Fprintln(stdout, out)
		return 0
	}
	ids := make([]int, len(items))
	for i, it := range items {
		ids[i] = it.ID
	}
	code := invalidateIDs(ctx, ms, stdout, ids, "invalidate-stale")
	if code != 0 {
		return code
	}
	return 0
}

// maintainInvalidateIDs — --invalidate --ids 스냅샷 경로. dry-run 재평가 없이
// 지정 id 고정 적용(plan-파일 패턴). 존재하지 않는 id는 exit 1.
func maintainInvalidateIDs(ctx context.Context, ms MaintStore, stdout io.Writer, ids []int) int {
	return invalidateIDs(ctx, ms, stdout, ids, "invalidate --ids")
}

// invalidateIDs — 공통 배치 무효화: 존재 확인 → UPDATE+Commit(Retry).
func invalidateIDs(ctx context.Context, ms MaintStore, stdout io.Writer, ids []int, op string) int {
	rows, err := queryOnce(ctx, ms,
		`SELECT id FROM knowledge WHERE id = ANY($1)`, []any{ids})
	if err != nil {
		fmt.Fprintln(stdout, errLine(err))
		return store.MapExit(err)
	}
	found := map[int]bool{}
	for _, r := range rows {
		if id, ok := cellToInt(r[0]); ok {
			found[id] = true
		}
	}
	var missing []int
	for _, id := range ids {
		if !found[id] {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		fmt.Fprintln(stdout, errLine(fmt.Errorf(
			"maintain: %s: 존재하지 않는 id %v", op, missing)))
		return 1
	}
	if err := retryWrite(ctx, func() error {
		if _, err := ms.Query(ctx, `
			UPDATE knowledge SET status = 'invalidated', invalid_at = now()
			WHERE id = ANY($1)`, ids); err != nil {
			return err
		}
		_, err := ms.Commit(ctx, fmt.Sprintf(
			"%s: %d건 무효화 (배치 무효화 갱신 경로) [session:0]", op, len(ids)))
		return err
	}); err != nil {
		fmt.Fprintln(stdout, errLine(err))
		return store.MapExit(err)
	}
	out, mErr := json.Marshal(struct {
		Status string `json:"status"`
		Count  int    `json:"count"`
		IDs    []int  `json:"ids"`
	}{Status: "invalidated", Count: len(ids), IDs: ids})
	if mErr != nil {
		fmt.Fprintln(stdout, errLine(mErr))
		return 1
	}
	fmt.Fprintln(stdout, string(out))
	return 0
}

// maintainSupersede — `--supersede OLD --by NEW`(spec §2 superseded).
// OLD가 active일 때만: status='invalidated'·superseded_by=NEW·
// invalid_at=NEW의 valid_at(Zep t_invalid=t_valid 준거). 쓰기는 Retry 동반.
// NEW 부재 → exit 1 · OLD==NEW → exit 3(무효 스펙) · OLD 비active → exit 1.
func maintainSupersede(ctx context.Context, ms MaintStore, stdout io.Writer, oldID, newID int) int {
	if oldID == newID {
		fmt.Fprintln(stdout, errLine(fmt.Errorf(
			"maintain: --supersede OLD와 --by NEW가 같음(id=%d)", oldID)))
		return 3
	}
	rows, err := queryOnce(ctx, ms, `
		SELECT (SELECT status FROM knowledge WHERE id = $1),
		       (SELECT valid_at FROM knowledge WHERE id = $2)`,
		[]any{oldID, newID})
	if err != nil {
		fmt.Fprintln(stdout, errLine(err))
		return store.MapExit(err)
	}
	var oldStatus string
	var newValidAt any
	if len(rows) > 0 && len(rows[0]) >= 2 {
		oldStatus, _ = rows[0][0].(string)
		newValidAt = rows[0][1]
	}
	if newValidAt == nil {
		fmt.Fprintln(stdout, errLine(fmt.Errorf(
			"maintain: --supersede: NEW 부재 id=%d", newID)))
		return 1
	}
	if oldStatus != "active" { // 부재(빈 상태) 포함 — active일 때만 승격
		fmt.Fprintln(stdout, errLine(fmt.Errorf(
			"maintain: --supersede: id=%d가 active 아님(상태 %q)", oldID, oldStatus)))
		return 1
	}
	if err := retryWrite(ctx, func() error {
		if _, err := ms.Query(ctx, `
			UPDATE knowledge
			SET status = 'invalidated',
			    superseded_by = $2,
			    invalid_at = (SELECT valid_at FROM knowledge WHERE id = $2)
			WHERE id = $1 AND status = 'active' AND invalid_at IS NULL`,
			oldID, newID); err != nil {
			return err
		}
		_, err := ms.Commit(ctx, fmt.Sprintf(
			"supersede: old=%d by=%d (대체 갱신 경로) [session:0]", oldID, newID))
		return err
	}); err != nil {
		fmt.Fprintln(stdout, errLine(err))
		return store.MapExit(err)
	}
	fmt.Fprintf(stdout, `{"status":"superseded","old":%d,"superseded_by":%d}`+"\n",
		oldID, newID)
	return 0
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
