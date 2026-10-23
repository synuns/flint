// status.go — flint status: ping + schema_version + pending 수를 한 줄 JSON으로.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"flint/lib/store"
)

// statusOutput — 키 순서 status·schema_version·pending으로 직렬화된다.
type statusOutput struct {
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

// statusAsOfOutput — T85: --as-of 지정 시 출력. 기존 statusOutput 필드를
// 임베딩해 키 순서를 그대로 두고 as_of{hash,commit_date}를 말미에 추가한다.
// as_of 형태는 read --as-of(asOfInfo)와 동일 — 소비자가 한 형태로 파싱한다
//(스펙 §1 "as_of 해시·시각"·e2e asOfHash 정합). statusOutput 자체는 불변
//(미지정 시 출력 불변 — v0.7 last_* 원칙 승계).
type statusAsOfOutput struct {
	statusOutput
	AsOf asOfInfo `json:"as_of"`
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

// runStatus — 서버 생존·스키마 호환·pending 대기 수를 보고한다.
// T85: --as-of <스펙> 지정 시 카운트 질의에 AS OF '<해시>'를 접합해 과거
// 스냅샷 pending 수를 보고하고 출력 말미에 as_of(정규화 해시)를 추가한다.
// 무효 스펙은 ErrInvalidAsOf → exit 3.
func runStatus(ctx context.Context, st Store, args []string, stdin io.Reader, stdout io.Writer) int {
	if err := st.Ping(ctx); err != nil {
		fmt.Fprintln(stdout, errLine(err))
		return store.MapExit(err)
	}
	asOfSpec := ""
	for i := 0; i < len(args); i++ {
		if args[i] == "--as-of" {
			if i+1 >= len(args) {
				// 값 없는 --as-of도 무효 스펙(빈 스펙)으로 exit 3.
				fmt.Fprintln(stdout, errLine(fmt.Errorf("빈 as-of 스펙: %w", ErrInvalidAsOf)))
				return 3
			}
			asOfSpec, i = args[i+1], i+1
		}
	}
	asOfHash, asOfDate := "", ""
	if asOfSpec != "" {
		hash, err := ParseAsOfSpec(ctx, st, asOfSpec)
		if err != nil {
			fmt.Fprintln(stdout, errLine(err))
			if errors.Is(err, ErrInvalidAsOf) {
				return 3
			}
			return store.MapExit(err)
		}
		asOfHash = hash
		// 커밋 시각 첨부(read --as-of와 동일 질의·조용한 폴백 — 메타데이터
		// 조회 실패 시 빈 값, §13).
		if dr, err := queryOnce(ctx, st,
			`SELECT date FROM dolt_log WHERE commit_hash = $1`, []any{hash}); err == nil &&
			len(dr) == 1 && len(dr[0]) > 0 {
			asOfDate = fmtAsOfDate(dr[0][0])
		}
	}
	rows, err := queryOnce(ctx, st, `SELECT value FROM meta WHERE key = 'schema_version'`, nil)
	if err != nil {
		fmt.Fprintln(stdout, errLine(err))
		return store.MapExit(err)
	}
	version := ""
	if len(rows) > 0 && rows[0][0] != nil {
		version = fmt.Sprint(rows[0][0])
	}
	// meta.schema_version 호환 검사(PLAN §10): 이 CLI가 아는 버전만 허용.
	// v2는 newRealStore의 EnsureSchemaV2 승격 후, v3는 EnsureSchemaV3 인덱스
	// 승격(T51·ADR-0008) 후, v4는 EnsureSchemaV4 계측 컬럼 승격(v0.6) 후,
	// v5는 EnsureSchemaV5 superseded_by 승격(T82·v0.8) 후 정상 상태다.
	if version != "1" && version != "2" && version != "3" && version != "4" && version != "5" {
		fmt.Fprintln(stdout, errLine(&store.SchemaError{Field: "schema_version", Expect: "1~5"}))
		return 3
	}
	// T85: as-of 지정 시 카운트 질의에만 AS OF 접합(해시는 dolt_log에서
	// 확정된 base32 소문자 해시라 인라인해도 안전 — T81/T83 룰링). meta·
	// 계측 조회는 현재 상태 그대로.
	countSQL := `SELECT count(*) FROM knowledge WHERE status = 'pending' AND invalid_at IS NULL`
	if asOfHash != "" {
		countSQL = `SELECT count(*) FROM knowledge AS OF '` + asOfHash +
			`' WHERE status = 'pending' AND invalid_at IS NULL`
	}
	rows, err = queryOnce(ctx, st, countSQL, nil)
	if err != nil {
		fmt.Fprintln(stdout, errLine(err))
		return store.MapExit(err)
	}
	pending := 0
	if len(rows) > 0 {
		fmt.Sscan(fmt.Sprint(rows[0][0]), &pending)
	}
	// T38 배선 회수(F-T56-1): status는 별도 프로세스이므로 in-memory 샘플이
	// 비어 있다 — meta hot_stats baseline을 로드해 직전 실행 누적을 보고한다.
	loadHotStatsFromMeta(ctx, st)
	calls, p95, _ := HotStats()
	readCalls, readZero := loadReadStatsFromMeta(ctx, st)
	// T63: 유입 3경로 last-seen(meta last_*_at, T60 기록) — 부재·파손은 null.
	getKV := metaGetKV(ctx, st)
	base := statusOutput{Status: "ok", SchemaVersion: version, Pending: pending,
		HotCalls: calls, HotP95: p95, ReadCalls: readCalls, ReadZeroPct: readZeroPct(readCalls, readZero),
		LastWriteAt: lastSeenOrNull(getKV, "last_write_at"),
		LastHotAt:   lastSeenOrNull(getKV, "last_hot_at"),
		LastReadAt:  lastSeenOrNull(getKV, "last_read_at")}
	var b []byte
	if asOfHash != "" {
		b, _ = json.Marshal(statusAsOfOutput{statusOutput: base,
			AsOf: asOfInfo{Hash: asOfHash, CommitDate: asOfDate}})
	} else {
		b, _ = json.Marshal(base)
	}
	fmt.Fprintln(stdout, string(b))
	return 0
}
