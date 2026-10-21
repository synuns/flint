// audit 커맨드 — 감사(Ruling: revert 커맨드화 폐기, flint audit 신설).
// `flint audit [--limit N] [--session ID]` — dolt_log 최근 N(기본 20·상한 100)을
// 컬럼 4종(commit_hash·committer·date·message)만 한 줄 JSON {"log":[...]}로 출력.
// --session ID는 message LIKE '%[session:ID]%' 필터. 서버 다운 exit 2(§13 조용히).
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"

	"flint/lib/store"
)

// audit 기본·상한 limit.
const (
	auditDefaultLimit = 20
	auditMaxLimit     = 100
)

// auditEntry — 감사 출력 최소 컬럼(토큰 예산 룰링: 4종만).
type auditEntry struct {
	CommitHash string `json:"commit_hash"`
	Committer  string `json:"committer"`
	Date       string `json:"date"`
	Message    string `json:"message"`
}

// RunAudit — dolt_log 최근 N 조회. limit은 0·음수 → 기본 20, 상한 100 clamp
// (무효 값 기본값 폴백 전역 제약).
func RunAudit(st Store, args []string, stdout io.Writer) int {
	ctx := context.Background()
	limit := auditDefaultLimit
	session := ""
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
		log = append(log, e)
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
