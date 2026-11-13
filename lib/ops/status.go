// status.go — flint status: ping + schema_version + pending 수를 한 줄 JSON으로.
// v0.40.0 T1: lib/cli에서 이동(패키지문·내보내기만 변경 — 동작 불변).
// windowedDaily(cli report.go — T3 이동 예정)·hot/read 계측 로더(cli
// metrics.go — T2 이동 예정)는 Fn seam으로 주입받는다.
package ops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/knowledge"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

// DailyRow — status 창 지표 산출에 필요한 일별 계측 열(report windowedDaily
// 호환 — T3 이동 전 seam 전달형).
type DailyRow struct {
	Read     int
	ReadZero int
	ReadNear int
}

// WindowedDailyFn — N일 창 일별 행 조회 seam. 실구현(cli report.go
// windowedDaily)은 cli/ops_alias_1.go init이 변환 래퍼로 주입한다.
var WindowedDailyFn func(ctx context.Context, st wire.Store, days int) ([]DailyRow, error)

// ReadZeroPctFn — zero 비율(정수 백분율, calls 0 가드) seam(cli metrics.go).
var ReadZeroPctFn func(calls, zero int) int

// HotStatsFn — 누적 hot 계측 노출 seam(cli metrics.go).
var HotStatsFn func() (calls int, p95 int, max int)

// LoadHotStatsFromMetaFn — meta hot_stats baseline 로드 seam(cli metrics.go).
var LoadHotStatsFromMetaFn func(ctx context.Context, st wire.Store)

// LoadReadStatsFromMetaFn — meta read_stats 로드 seam(cli metrics.go).
var LoadReadStatsFromMetaFn func(ctx context.Context, st wire.Store) (calls int, zero int)

// StatusOutput — 키 순서 status·schema_version·pending으로 직렬화된다.
type StatusOutput struct {
	Status        string  `json:"status"`
	SchemaVersion string  `json:"schema_version"`
	Pending       int     `json:"pending"`
	HotCalls      int     `json:"hot_calls"`
	HotP95        int     `json:"hot_p95"`
	ReadCalls     int     `json:"read_calls"`
	ReadZeroPct   int     `json:"read_zero_pct"`
	LastWriteAt   *string `json:"last_write_at"`
	LastHotAt     *string `json:"last_hot_at"`
	LastReadAt    *string `json:"last_read_at"`
}

// statusV096Output — v0.9.6: StatusOutput 필드를 임베딩해 키 순서를 그대로
// 두고 read_fallback_calls(T1 계측 meta 노출)를 말미에 추가한다. 미설정은
// null(v0.6 last_* 패턴 승계). StatusOutput 자체는 불변(기존 필드·순서
// 계약 — status_v06/v08 리플렉션 테스트가 담보).
type statusV096Output struct {
	StatusOutput
	ReadFallbackCalls *int `json:"read_fallback_calls"`
}

// statusV0180Output — v0.18.0 R4: 기간화 read 지표 추가. 누적 read_calls·
// read_zero_pct는 초기 이력에 지배돼 현행 상태를 오독하게 한다(누적과
// report 기간화 값이 크게 갈린 사례) — 14일 일별 합산 창 값을 말미에
// 병기한다(report zero_pct와 동일 산식). 기존 키·순서 불변(additive).
type statusV0180Output struct {
	statusV096Output
	ReadCalls14d       int `json:"read_calls_14d"`
	ReadZeroPct14d     int `json:"read_zero_pct_14d"`
	ReadZeroNearPct14d int `json:"read_zero_near_pct_14d"`
	// v0.24.0 리뷰 ④: *_14d 명칭은 역호환 유지, 실제 창은 이 필드로
	// 표시(daily_retention_days 오버라이드 반영 — 거짓 이름 방어).
	ReadWindowDays int `json:"read_window_days"`
	// v1.1.0 G5 최소 주입 — hot(주입)·read 출력 바이트의 창 합산
	// (hot_budget 준수 실측 감시, METRICS §4 G5). additive 말미 추가.
	HotBytes14d  int `json:"hot_bytes_14d"`
	ReadBytes14d int `json:"read_bytes_14d"`
}

// (v0.24.0 R1b: statusWindowedDays 상수 폐지 — 보존 창과 단일화.)

// StatusWindowedRead — N일 창 일별 합산으로 read·zero%/near%(버림 정수%).
// v0.18.1: 파서는 windowedDaily 공유 리더(report와 단일 소스). 조회 실패는
// 전부 0(계측 표시 — status를 깨지 않는다, FallbackCallsOrNull와 같은 원칙).
func StatusWindowedRead(ctx context.Context, st wire.Store, days int) (read, zeroPct, nearPct int) {
	dayRows, err := WindowedDailyFn(ctx, st, days)
	if err != nil {
		return 0, 0, 0
	}
	var zero, near int
	for _, d := range dayRows {
		read += d.Read
		zero += d.ReadZero
		near += d.ReadNear
	}
	return read, ReadZeroPctFn(read, zero), ReadZeroPctFn(read, near)
}

// StatusWindowedBytes — v1.1.0 G5: N일 창 hot(주입)·read 출력 바이트
// 합산. WindowedDaily 실구현 직속 호출(파서 단일 소스 — seam DailyRow는
// read 계측만 전달하므로 bytes는 여기서 직접 합산). 조회 실패·파손은
// 전부 0(계측 표시 — status를 깨지 않는다, StatusWindowedRead와 동일
// 원칙).
func StatusWindowedBytes(ctx context.Context, st wire.Store, days int) (hotBytes, readBytes int) {
	rows, err := WindowedDaily(ctx, st, days)
	if err != nil {
		return 0, 0
	}
	for _, d := range rows {
		hotBytes += d.HotBytes
		readBytes += d.ReadBytes
	}
	return hotBytes, readBytes
}

// statusAsOfOutput — T85: --as-of 지정 시 출력. 기존 StatusOutput 필드를
// 임베딩해 키 순서를 그대로 두고 as_of{hash,commit_date}를 말미에 추가한다.
// v0.9.6부터는 statusV096Output(끝에 read_fallback_calls)을, v0.18.0부터는
// statusV0180Output(끝에 기간화 read 지표)을 임베딩한다.
// as_of 형태는 read --as-of(asOfInfo)와 동일 — 소비자가 한 형태로 파싱한다
// (스펙 §1 "as_of 해시·시각"·e2e asOfHash 정합). StatusOutput 자체는 불변
// (미지정 시 출력 불변 — v0.7 last_* 원칙 승계).
type statusAsOfOutput struct {
	statusV0180Output
	AsOf knowledge.AsOfInfo `json:"as_of"`
}

// FallbackCallsOrNull — meta read_fallback_calls(T1 폴백 계측) 조회.
// 부재·파손(비정수)·조회 오류는 nil(JSON null) — 계측 표시이므로 status를
// 깨지 않는다(lastSeenOrNull와 같은 원칙).
func FallbackCallsOrNull(getKV func(string, string) (string, error)) *int {
	v, err := getKV("read_fallback_calls", "")
	if err != nil || v == "" {
		return nil
	}
	n, perr := strconv.Atoi(strings.TrimSpace(v))
	if perr != nil {
		return nil
	}
	return &n
}

// lastSeenOrNull — meta에서 RFC3339 last_* 키 조회. 키 부재·파손(비-RFC3339)은
// nil(JSON null). 계측 표시이므로 조회 오류도 조용히 null로 폴백한다.
func lastSeenOrNull(getKV func(string, string) (string, error), key string) *string {
	v, err := getKV(key, "")
	if err != nil || v == "" {
		return nil
	}
	if _, perr := time.Parse(time.RFC3339, strings.TrimSpace(v)); perr != nil {
		return nil
	}
	s := strings.TrimSpace(v)
	return &s
}

// lastReadAtOrNull — v0.42.0 T2: read 경로가 meta last_read_at을 더 쓰지
// 않는다(쓰기 4→2 — RecordReadUsage 폐지). 잔존 meta 값과 read_events
// 최근 시각(max(ts)) 중 더 최근 쪽을 보고한다(doctor checkLastSeen과
// 동일 병합 룰). 조회 실패·매핑 불가는 기존 meta 값 유지(조용히).
func lastReadAtOrNull(ctx context.Context, st wire.Store, getKV func(string, string) (string, error)) *string {
	out := lastSeenOrNull(getKV, "last_read_at")
	rows, err := wire.QueryOnce(ctx, st, `SELECT max(ts) FROM read_events`, nil)
	if err == nil && len(rows) > 0 && len(rows[0]) > 0 && rows[0][0] != nil {
		if t, ok := CellToTime(rows[0][0]); ok {
			s := t.UTC().Format(time.RFC3339)
			if out == nil {
				out = &s
			} else if prev, perr := time.Parse(time.RFC3339, *out); perr == nil && t.After(prev) {
				out = &s
			}
		}
	}
	return out
}

// RunStatus — 서버 생존·스키마 호환·pending 대기 수를 보고한다.
// T85: --as-of <스펙> 지정 시 카운트 질의에 AS OF '<해시>'를 접합해 과거
// 스냅샷 pending 수를 보고하고 출력 말미에 as_of(정규화 해시)를 추가한다.
// 무효 스펙은 ErrInvalidAsOf → exit 3.
func RunStatus(ctx context.Context, st wire.Store, args []string, stdin io.Reader, stdout io.Writer) int {
	if err := st.Ping(ctx); err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	asOfSpec := ""
	for i := 0; i < len(args); i++ {
		if args[i] == "--as-of" {
			if i+1 >= len(args) {
				// 값 없는 --as-of도 무효 스펙(빈 스펙)으로 exit 3.
				fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf("빈 as-of 스펙: %w", ErrInvalidAsOf)))
				return 3
			}
			asOfSpec, i = args[i+1], i+1
		}
	}
	asOfHash, asOfDate := "", ""
	if asOfSpec != "" {
		hash, err := ParseAsOfSpecFn(ctx, st, asOfSpec)
		if err != nil {
			fmt.Fprintln(stdout, wire.ErrLine(err))
			if errors.Is(err, ErrInvalidAsOf) {
				return 3
			}
			return store.MapExit(err)
		}
		asOfHash = hash
		// 커밋 시각 첨부(read --as-of와 동일 질의·조용한 폴백 — 메타데이터
		// 조회 실패 시 빈 값, §13).
		if dr, err := wire.QueryOnce(ctx, st,
			`SELECT date FROM dolt_log WHERE commit_hash = $1`, []any{hash}); err == nil &&
			len(dr) == 1 && len(dr[0]) > 0 {
			asOfDate = knowledge.FmtAsOfDate(dr[0][0])
		}
	}
	rows, err := wire.QueryOnce(ctx, st, `SELECT value FROM meta WHERE key = 'schema_version'`, nil)
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	version := ""
	if len(rows) > 0 && rows[0][0] != nil {
		version = fmt.Sprint(rows[0][0])
	}
	// meta.schema_version 호환 검사(PLAN §10): 이 CLI가 아는 버전만 허용.
	// 허용 범위는 db.LatestSchemaVersion에서 파생한다(1..최신 — 각 버전의
	// 승격 순서는 db.SchemaGates 연속성이 담보). 하드코딩 목록은 스키마
	// 승격마다 낡는다(v9 DB에서 정상 상태인데 exit 3 — 감사 F1).
	ver, perr := strconv.Atoi(strings.TrimSpace(version))
	if perr != nil || ver < 1 || ver > db.LatestSchemaVersion {
		fmt.Fprintln(stdout, wire.ErrLine(&store.SchemaError{Field: "schema_version",
			Expect: "1~" + strconv.Itoa(db.LatestSchemaVersion)}))
		return 3
	}
	// v0.18.0 R4: 기간화 read 지표(창 = daily_retention_days, v0.24.0) —
	// 누적값과 복기. *_14d 필드명은 역호환 유지(실제 창은 read_window_days).
	winDays := knowledge.WriteThresholds(ctx, st).DailyRetentionD
	winRead, winZeroPct, winNearPct := StatusWindowedRead(ctx, st, winDays)
	// v1.1.0 G5 — hot·read 출력 바이트 창 합산(조회 실패는 0 폴백).
	winHotBytes, winReadBytes := StatusWindowedBytes(ctx, st, winDays)
	// T85: as-of 지정 시 카운트 질의에만 AS OF 접합. meta·계측 조회는
	// 현재 상태 그대로. 현재 시점 카운트는 countPending 단일
	// 구현(v0.34.3 3중 쿼리 복제 수렴).
	pending := 0
	if asOfHash != "" {
		// v0.28.0 H③ 승계(read --as-of와 동일 규칙): 접합 전 해시 문자셋을
		// 재검증한다 — dolt_log 엔진 반환값을 신뢰해 SQL에 인라인하지
		// 않는다. 위반은 오류 종료(오염 해시 접합 주입 차단).
		if !knowledge.ValidAsOfHash(asOfHash) {
			fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf("as-of hash 문자셋 위반: %q", asOfHash)))
			return 1
		}
		rows, err = wire.QueryOnce(ctx, st, `SELECT count(*) FROM knowledge AS OF '`+asOfHash+
			`' WHERE status = 'pending' AND invalid_at IS NULL`, nil)
		if err != nil {
			fmt.Fprintln(stdout, wire.ErrLine(err))
			return store.MapExit(err)
		}
		if len(rows) > 0 {
			fmt.Sscan(fmt.Sprint(rows[0][0]), &pending)
		}
	} else {
		n, cerr := knowledge.CountPending(ctx, st)
		if cerr != nil {
			fmt.Fprintln(stdout, wire.ErrLine(cerr))
			return store.MapExit(cerr)
		}
		pending = n
	}
	// T38 배선 회수(F-T56-1): status는 별도 프로세스이므로 in-memory 샘플이
	// 비어 있다 — meta hot_stats baseline을 로드해 직전 실행 누적을 보고한다.
	LoadHotStatsFromMetaFn(ctx, st)
	calls, p95, _ := HotStatsFn()
	readCalls, readZero := LoadReadStatsFromMetaFn(ctx, st)
	// T63: 유입 3경로 last-seen(meta last_*_at, T60 기록) — 부재·파손은 null.
	getKV := knowledge.MetaGetKV(ctx, st)
	base := statusV096Output{StatusOutput: StatusOutput{Status: "ok", SchemaVersion: version, Pending: pending,
		HotCalls: calls, HotP95: p95, ReadCalls: readCalls, ReadZeroPct: ReadZeroPctFn(readCalls, readZero),
		LastWriteAt: lastSeenOrNull(getKV, "last_write_at"),
		LastHotAt:   lastSeenOrNull(getKV, "last_hot_at"),
		// v0.42.0 T2: last_read_at은 meta 잔존값과 read_events 파생 병합.
		LastReadAt: lastReadAtOrNull(ctx, st, getKV)},
		ReadFallbackCalls: FallbackCallsOrNull(getKV)}
	var b []byte
	if asOfHash != "" {
		b, _ = json.Marshal(statusAsOfOutput{statusV0180Output: statusV0180Output{statusV096Output: base,
			ReadCalls14d: winRead, ReadZeroPct14d: winZeroPct, ReadZeroNearPct14d: winNearPct, ReadWindowDays: winDays,
			HotBytes14d: winHotBytes, ReadBytes14d: winReadBytes},
			AsOf: knowledge.AsOfInfo{Hash: asOfHash, CommitDate: asOfDate}})
	} else {
		b, _ = json.Marshal(statusV0180Output{statusV096Output: base,
			ReadCalls14d: winRead, ReadZeroPct14d: winZeroPct, ReadZeroNearPct14d: winNearPct, ReadWindowDays: winDays,
			HotBytes14d: winHotBytes, ReadBytes14d: winReadBytes})
	}
	fmt.Fprintln(stdout, string(b))
	return 0
}
