// audit 커맨드 — 감사(Ruling: revert 커맨드화 폐지, flint audit 신설).
// `flint audit [--limit N] [--session ID] [--stat] [--telemetry]` — dolt_log 최근
// N(기본 20·상한 100)을 컬럼 4종(commit_hash·committer·date·message)만 한 줄
// JSON {"log":[...]}로 출력. --session ID는 message LIKE '%[session:ID]%' 필터.
// 기본 쿼리는 telemetry 커밋(message이 'telemetry: ' 접두 —
// CommitTelemetry 계약)을 NOT LIKE로 제외하고 --telemetry 플래그로 포함한다.
// 서버 다운 exit 2(§13 조용히).
package ops

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/synuns/flint/lib/query"
	"github.com/synuns/flint/lib/render"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

// audit 기본·상한 limit·사용법 행.
const (
	auditDefaultLimit = 20
	auditMaxLimit     = 100
	auditUsage        = "usage: flint audit [--limit N] [--session ID] [--stat] [--telemetry]"
)

// auditEntry — 감사 출력 최소 컬럼(토큰 예산 룰링: 4종만).
// --stat 전용 부록 필드(stat·date_rel)는 omitempty — 기본 출력 키 불변.
type auditEntry struct {
	CommitHash string                    `json:"commit_hash"`
	Committer  string                    `json:"committer"`
	Date       string                    `json:"date"`
	Message    string                    `json:"message"`
	Stat       map[string]auditTableStat `json:"stat,omitempty"`
	DateRel    string                    `json:"date_rel,omitempty"`
}

// auditTableStat — dolt_diff 테이블별 변경 요약(--stat 부록).
type auditTableStat struct {
	DataChange   bool `json:"data_change"`
	SchemaChange bool `json:"schema_change"`
}

// RelTime — t를 now 대비 상대 시각으로("3h ago"·"2d ago" — log 시각 옵션
// 부재로 자체 포맷, C13). 1시간 미만은 분, 24시간 미만은 시, 이후는 일(내림).
// 미래 시각은 0으로 클램프.
func RelTime(t time.Time) string {
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

// RunAudit — dolt_log 최근 N 조회. limit은 0·음수 → 기본 20, 상한 100 clamp
// (무효 값 기본값 폴백 전역 제약). 인자 검증 경로(unknown flag·
// 값 누락·비수 값)는 무효 인자 exit 3 — --limit 비수는 read·brief(--limit
// Atoi 실패 exit 3)와 동일 규칙. 수치형 0·음수만 조용한 clamp 폴백
// (read ClampLimit 관례).
func RunAudit(st wire.Store, args []string, stdout io.Writer) int {
	ctx := context.Background()
	// 위치 인자 불허(wire.ParseArgs 사양 테이블).
	// 값 검증은 발생 순(AllOccs) 재현 — 기존 오류 우선 순서 그대로.
	res, perr := wire.ParseArgs(args, wire.ArgSpec{
		Flags: []wire.FlagSpec{
			{Name: "--limit", Value: true},
			{Name: "--stat"},
			{Name: "--session", Value: true},
			{Name: "--telemetry"},
		},
		Usage: auditUsage,
	})
	if perr != nil {
		fmt.Fprintln(stdout, wire.ErrLine(perr))
		return 3
	}
	limit := auditDefaultLimit
	session := res.Value("--session")
	stat := res.Given("--stat")
	telemetry := res.Given("--telemetry")
	for _, o := range res.AllOccs() {
		switch o.Name {
		case "--limit":
			if !o.HasValue {
				fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf("--limit requires a number — "+auditUsage)))
				return 3
			}
			n, err := strconv.Atoi(o.Value)
			if err != nil {
				fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf("invalid limit %q — "+auditUsage, o.Value)))
				return 3
			}
			if n < 1 {
				n = auditDefaultLimit // 수치형 0·음수 폴백(에러 아님)
			}
			limit = n
		case "--session":
			if !o.HasValue || o.Value == "" {
				fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf("--session requires an id — "+auditUsage)))
				return 3
			}
		}
	}
	if limit > auditMaxLimit {
		limit = auditMaxLimit
	}

	// v1.7.2 T2(→ v1.8.0 T8 Store 이관): 기본은 telemetry 커밋('telemetry: '
	// 접두 — CommitTelemetry 계약)을 MessageNotLike로 제외, --telemetry
	// 플래그로 포함. --session 필터는 MessageLike('%[session:ID]%' 패턴)로
	// AND 결합 — 세 조건 모두 DB 측 조건으로 전달된다(방언 SQL은 lib/db).
	q := wire.LogQuery{LatestN: limit}
	if !telemetry {
		q.MessageNotLike = `telemetry: %`
	}
	if session != "" {
		// LIKE 메타문자(%·_) 포함 세션 ID는 리터럴 일치로만 매칭 —
		// query.EscapeLike로 이스케이프(값 자체는 계속 파라미터화).
		q.MessageLike = `%[session:` + query.EscapeLike(session) + `]%`
	}
	entries, err := st.LogEntries(ctx, q)
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	log := make([]auditEntry, 0, len(entries)) // 0행도 "log":[] 보장
	for _, en := range entries {
		e := auditEntry{}
		// LogEntry 문자열 필드는 전부 render.Text 재살균 —
		// message뿐 아니라 hash·committer도 원문 노출 경로다.
		e.CommitHash = render.Text(en.Hash)
		e.Committer = render.Text(en.Committer)
		if !en.Date.IsZero() {
			e.Date = fmt.Sprint(en.Date)
		}
		e.Message = render.Text(en.Message) // render.Text 경유(엔진 커밋 로그 message 재살균 승계)
		if stat && !en.Date.IsZero() {
			e.DateRel = RelTime(en.Date)
		}
		log = append(log, e)
	}
	if stat && len(log) > 0 {
		// 테이블별 data_change·schema_change 요약 부록 — 커밋당 1회씩
		// 묻는 N+1(상한 100 커밋 = 100왕복) 대신 해시 목록 단일 배치
		// 조회(DiffStat — 방언 SQL은 lib/db).
		hashes := make([]string, 0, len(log))
		for i := range log {
			if log[i].CommitHash != "" {
				hashes = append(hashes, log[i].CommitHash)
			}
		}
		diffStats, err := st.DiffStat(ctx, hashes)
		if err != nil {
			fmt.Fprintln(stdout, wire.ErrLine(err))
			return store.MapExit(err)
		}
		stats := make(map[string]map[string]auditTableStat, len(log))
		for _, ds := range diffStats {
			// DiffStat CommitHash·Table도 살균 —
			// stats 맵 키는 살균된 log 측 CommitHash와 매칭되므로 같은
			// 출구(render.Text)를 통과시켜 일관성을 유지한다.
			h := render.Text(ds.CommitHash)
			ts := auditTableStat{DataChange: ds.DataChange, SchemaChange: ds.SchemaChange}
			if stats[h] == nil {
				stats[h] = map[string]auditTableStat{}
			}
			stats[h][render.Text(ds.Table)] = ts
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

// runAudit — registry 핸들러 래퍼(approve.go 패턴 — RunAudit는 ctx·stdin을
// 사용하지 않는다).
func runAudit(ctx context.Context, st wire.Store, args []string, stdin io.Reader, stdout io.Writer) int {
	return RunAudit(st, args, stdout)
}
