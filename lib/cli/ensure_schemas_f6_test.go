// ensure_schemas_f6_test.go — F6 (performance) regression: on a latest DB, ensureSchemas runs
// only the 1 schema_version probe and skips the whole 8-gate walk (db.SchemaGates —
// 16 round trips per connection when each gate re-queried the version).
// Passing a nil repo makes any walk revival immediately visible (a gate touching the nil repo
// panics — the latest shortcut is the only passing path).
package cli

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/db"
)

// TestEnsureSchemasLatestSkipsGates — when the probe confirms latest
// (LatestSchemaVersion), only 1 query is issued (gate walk skipped).
func TestEnsureSchemasLatestSkipsGates(t *testing.T) {
	st := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "schema_version") {
			return [][]any{{fmt.Sprint(db.LatestSchemaVersion)}}, nil // LatestSchemaVersion
		}
		t.Errorf("예상 밖 쿼리(게이트 순회 누출): %s", sql)
		return nil, nil
	}}
	latest, err := ensureSchemas(context.Background(), nil, st)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !latest {
		t.Fatal("latest = false, want true(최신 probe)")
	}
	if n := len(st.queries); n != 1 {
		t.Fatalf("쿼리 %d건, want 1(schema_version probe만 — 게이트 8종 순회 스킵)", n)
	}
}

// TestSchemaVersionFnParsing — probe verdict: integer latest -> (9,true); below latest,
// non-integer, empty results, or lookup errors -> (...,false), falling back to the sequential promotion path.
func TestSchemaVersionFnParsing(t *testing.T) {
	cases := []struct {
		name string
		rows [][]any
		err  bool
		v    int
		ok   bool
	}{
		{"최신 버전", [][]any{{"9"}}, false, 9, true},
		{"레거시 버전", [][]any{{"3"}}, false, 3, true},
		{"비정수", [][]any{{"beta"}}, false, 0, false},
		{"빈 결과", nil, false, 0, false},
		{"조회 오류", nil, true, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
				if tc.err {
					return nil, context.Canceled
				}
				return tc.rows, nil
			}}
			v, ok := schemaVersionFn(context.Background(), st)
			if v != tc.v || ok != tc.ok {
				t.Fatalf("(%d,%v), want (%d,%v)", v, ok, tc.v, tc.ok)
			}
		})
	}
	// boundary: below LatestSchemaVersion remains a gate-walk target (even with ok=true,
	// ensureSchemas's shortcut condition v >= LatestSchemaVersion filters it out).
	if _, ok := schemaVersionFn(context.Background(), &fakeStore{
		queryFn: func(string, []any) ([][]any, error) {
			return [][]any{{fmt.Sprint(db.LatestSchemaVersion - 1)}}, nil
		},
	}); !ok {
		t.Fatal("정수(최신-1)은 ok=true여야 한다(레거시 판정은 ensureSchemas가 소유)")
	}
}
