// audit 커맨드 — 감사(Ruling: revert 커맨드화 폐기, flint audit 신설).
// `flint audit [--limit N] [--session ID] [--stat]` — dolt_log 최근 N(기본 20·상한 100)을
// 컬럼 4종(commit_hash·committer·date·message)만 한 줄 JSON {"log":[...]}로 출력.
// --session ID는 message LIKE '%[session:ID]%' 필터. 서버 다운 exit 2(§13 조용히).
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"time"

	"flint/lib/store"
)

// audit 기본·상한 limit.
const (
	auditDefaultLimit = 20
	auditMaxLimit     = 100
)

// auditEntry — 감사 출력 최소 컬럼(토큰 예산 룰링: 4종만).
// v0.8 --stat 전용 부록 필드(stat·date_rel)는 omitempty — 기본 출력 키 불변.
type auditEntry struct {
	CommitHash string `json:"commit_hash"`
	Committer  string `json:"committer"`
	Date       string `json:"date"`
	Message    string `json:"message"`
	Stat       map[string]auditTableStat `json:"stat,omitempty"`
	DateRel    string                    `json:"date_rel,omitempty"`
}

// auditTableStat — dolt_diff 테이블별 변경 요약(--stat 부록).
type auditTableStat struct {
	DataChange   bool `json:"data_change"`
	SchemaChange bool `json:"schema_change"`
}

// relTime — t를 now 대비 상대 시각으로("3h ago"·"2d ago" — log 시각 옵션
// 부재로 자체 포맷, C13). 1시간 미만은 분, 24시간 미만은 시, 이후는 일(내림).
// 미래 시각은 0으로 클램프.
func relTime(t time.Time) string {
	d := time.Since(t)
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

// parseAuditDate — dolt_log date 값을 time.Time으로(실구현은 time.Time,
// fake/문자열은 레이아웃 3종 파싱). 실패 시 date_rel 미기재.
func parseAuditDate(v any) (time.Time, bool) {
	switch x := v.(type) {
	case time.Time:
		return x, true
	case string:
		for _, lay := range []string{
			"2006-01-02 15:04:05.999999999 -0700 MST", // time.Time 기본 Sprint
			"2006-01-02 15:04:05.999999999",
			time.RFC3339,
		} {
			if ts, err := time.Parse(lay, x); err == nil {
				return ts, true
			}
		}
	}
	return time.Time{}, false
}

// RunAudit — dolt_log 최근 N 조회. limit은 0·음수 → 기본 20, 상한 100 clamp
// (무효 값 기본값 폴백 전역 제약).
func RunAudit(st Store, args []string, stdout io.Writer) int {
	ctx := context.Background()
	limit := auditDefaultLimit
	session := ""
	stat := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--limit":
			i++
			if i >= len(args) {
				fmt.Fprintln(stdout, errLine(fmt.Errorf("--limit requires a number")))
				return 1
			}
			n, err := strconv.Atoi(args[i])
			if err != nil || n < 1 {
				n = auditDefaultLimit // 무효 값 폴백(에러 아님)
			}
			limit = n
		case "--stat":
			stat = true
		case "--session":
			i++
			if i >= len(args) || args[i] == "" {
				fmt.Fprintln(stdout, errLine(fmt.Errorf("--session requires an id")))
				return 1
			}
			session = args[i]
		default:
			fmt.Fprintln(stdout, errLine(fmt.Errorf("unknown flag: %s", args[i])))
			return 1
		}
	}
	if limit > auditMaxLimit {
		limit = auditMaxLimit
	}

	sql := `SELECT commit_hash, committer, date, message FROM dolt_log`
	qargs := []any{limit}
	if session != "" {
		sql += ` WHERE message LIKE $2`
		qargs = append(qargs, `%[session:`+session+`]%`)
	}
	sql += ` ORDER BY commit_order DESC LIMIT $1`

	rows, err := queryOnce(ctx, st, sql, qargs)
	if err != nil {
		fmt.Fprintln(stdout, errLine(err))
		return store.MapExit(err)
	}
	log := make([]auditEntry, 0, len(rows)) // 0행도 "log":[] 보장
	for _, r := range rows {
		e := auditEntry{}
		if len(r) > 0 {
			e.CommitHash = fmt.Sprint(r[0])
		}
		if len(r) > 1 {
			e.Committer = fmt.Sprint(r[1])
		}
		if len(r) > 2 && r[2] != nil {
			e.Date = fmt.Sprint(r[2])
		}
		if len(r) > 3 {
			e.Message = fmt.Sprint(r[3])
		}
		if stat {
			if t, ok := parseAuditDate(r[2]); ok {
				e.DateRel = relTime(t)
			}
		}
		log = append(log, e)
	}
	if stat && len(log) > 0 {
		// dolt_diff 테이블별 data_change·schema_change 요약 부록 — 커밋당
		// 1회씩 묻는 N+1(상한 100 커밋 = 100왕복) 대신 해시 목록 ANY($1)
		// 단일 배치 질의로 한 번에 조회한다.
		hashes := make([]string, 0, len(log))
		for i := range log {
			if log[i].CommitHash != "" {
				hashes = append(hashes, log[i].CommitHash)
			}
		}
		diffRows, err := queryOnce(ctx, st,
			`SELECT commit_hash, table_name, data_change, schema_change
FROM dolt_diff WHERE commit_hash = ANY($1)`, []any{hashes})
		if err != nil {
			fmt.Fprintln(stdout, errLine(err))
			return store.MapExit(err)
		}
		stats := make(map[string]map[string]auditTableStat, len(log))
		for _, dr := range diffRows {
			if len(dr) < 4 {
				continue
			}
			h := fmt.Sprint(dr[0])
			ts := auditTableStat{}
			if b, ok := dr[2].(bool); ok {
				ts.DataChange = b
			}
			if b, ok := dr[3].(bool); ok {
				ts.SchemaChange = b
			}
			if stats[h] == nil {
				stats[h] = map[string]auditTableStat{}
			}
			stats[h][fmt.Sprint(dr[1])] = ts
		}
		for i := range log {
			if s, ok := stats[log[i].CommitHash]; ok {
				log[i].Stat = s
			}
		}
	}
	b, _ := json.Marshal(map[string]any{"log": log})
	fmt.Fprintln(stdout, string(b))
	return 0
}

// runAudit — registry 핸들러(approve.go 패턴). ctx·stdin은 사용하지 않는다.
func runAudit(ctx context.Context, st Store, args []string, stdin io.Reader, stdout io.Writer) int {
	return RunAudit(st, args, stdout)
}

func init() { Register("audit", runAudit) }
