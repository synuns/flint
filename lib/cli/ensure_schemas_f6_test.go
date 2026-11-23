package cli

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/db"
)

func TestEnsureSchemasLatestSkipsGates(t *testing.T) {
	st := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "schema_version") {
			return [][]any{{fmt.Sprint(db.LatestSchemaVersion)}}, nil
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

	if _, ok := schemaVersionFn(context.Background(), &fakeStore{
		queryFn: func(string, []any) ([][]any, error) {
			return [][]any{{fmt.Sprint(db.LatestSchemaVersion - 1)}}, nil
		},
	}); !ok {
		t.Fatal("정수(최신-1)은 ok=true여야 한다(레거시 판정은 ensureSchemas가 소유)")
	}
}
