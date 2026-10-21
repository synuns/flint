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
	// meta.schema_version 호환 검사: v0.1 계약은 "1".
	if version != "1" {
		fmt.Fprintln(stdout, errLine(&store.SchemaError{Field: "schema_version", Expect: "1"}))
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
	calls, p95, _ := HotStats()
	b, _ := json.Marshal(statusOutput{Status: "ok", SchemaVersion: version, Pending: pending,
		HotCalls: calls, HotP95: p95})
	fmt.Fprintln(stdout, string(b))
	return 0
}
