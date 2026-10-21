// read.go — flint read [query]: active_knowledge 뷰 강제 조회 + 토큰 예산.
package cli

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"

	"flint/lib/query"
	"flint/lib/store"
)

// runRead — 무료 텍스트 query와 --tag --entity --domain --limit 필터.
// --kind는 active_knowledge가 knowledge 단일 kind 뷰라 의미가 없어 받지 않는다.
func runRead(ctx context.Context, st Store, args []string, stdin io.Reader, stdout io.Writer) int {
	f := query.Filters{}
	jsonMode, fields := false, ""
	var pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		var v string
		if i+1 < len(args) {
			v = args[i+1]
		}
		switch a {
		case "--tag":
			f.Tag, i = v, i+1
		case "--entity":
			f.Entity, i = v, i+1
		case "--domain":
			f.Domain, i = v, i+1
		case "--limit":
			n, err := strconv.Atoi(v)
			if err != nil {
				fmt.Fprintln(stdout, errLine(&store.SchemaError{Field: "limit", Expect: "integer"}))
				return 3
			}
			f.Limit, i = n, i+1
		case "--json":
			jsonMode = true
		case "--fields":
			fields, i = v, i+1
		default:
			pos = append(pos, a)
		}
	}
	sql, qargs := query.BuildRead(strings.Join(pos, " "), f)
	rows, err := queryOnce(ctx, st, sql, qargs)
	if err != nil {
		fmt.Fprintln(stdout, errLine(err))
		return store.MapExit(err)
	}
	fmt.Fprint(stdout, query.FormatRows(rows, fields, jsonMode))
	return 0
}
