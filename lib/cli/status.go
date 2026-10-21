// status.go — flint status: ping + schema_version + pending 수를 한 줄 JSON으로.
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"flint/lib/store"
)

// statusOutput — 키 순서 status·schema_version·pending으로 직렬화된다.
type statusOutput struct {
	Status        string `json:"status"`
	SchemaVersion string `json:"schema_version"`
	Pending       int    `json:"pending"`
	HotCalls      int    `json:"hot_calls"`
	HotP95        int    `json:"hot_p95"`
	ReadCalls     int    `json:"read_calls"`
	ReadZeroPct   int    `json:"read_zero_pct"`
}

// runStatus — 서버 생존·스키마 호환·pending 대기 수를 보고한다.
func runStatus(ctx context.Context, st Store, args []string, stdin io.Reader, stdout io.Writer) int {
	if err := st.Ping(ctx); err != nil {
		fmt.Fprintln(stdout, errLine(err))
		return store.MapExit(err)
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
	// 승격(T51·ADR-0008, 무손실) 후 정상 상태다.
	if version != "1" && version != "2" && version != "3" {
		fmt.Fprintln(stdout, errLine(&store.SchemaError{Field: "schema_version", Expect: "1~3"}))
		return 3
	}
	rows, err = queryOnce(ctx, st,
		`SELECT count(*) FROM knowledge WHERE status = 'pending' AND invalid_at IS NULL`, nil)
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
	b, _ := json.Marshal(statusOutput{Status: "ok", SchemaVersion: version, Pending: pending,
		HotCalls: calls, HotP95: p95, ReadCalls: readCalls, ReadZeroPct: readZeroPct(readCalls, readZero)})
	fmt.Fprintln(stdout, string(b))
	return 0
}
