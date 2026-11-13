// ensure_schemas_f6_test.go — F6(성능) 회귀: 최신 DB에서 ensureSchemas는
// schema_version probe 1회만 실행하고 게이트 8종 순회(db.SchemaGates —
// 각 게이트가 버전을 재조회하던 접속당 16왕복)를 통째로 건너뛴다.
// repo에 nil을 주어 순회가 살아나면 즉시 드러난다(게이트가 nil repo를
// 건드려 패닉 — 최신 단축이 유일한 통과 경로).
package cli

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/db"
)

// TestEnsureSchemasLatestSkipsGates — Review Focus: probe가 최신
// (LatestSchemaVersion)을 확인하면 쿼리 1건만 발행(게이트 순회 스킵).
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

// TestSchemaVersionFnParsing — probe 판정: 정수 최신 → (9,true), 미달·
// 비정수·빈 결과·조회 오류 → (…,false)로 순차 승격 경로에 폴백.
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
	// 경계: LatestSchemaVersion 미달은 게이트 순회 대상(ok=true여도
	// ensureSchemas의 단축 조건 v >= LatestSchemaVersion에서 걸러진다).
	if _, ok := schemaVersionFn(context.Background(), &fakeStore{
		queryFn: func(string, []any) ([][]any, error) {
			return [][]any{{fmt.Sprint(db.LatestSchemaVersion - 1)}}, nil
		},
	}); !ok {
		t.Fatal("정수(최신-1)은 ok=true여야 한다(레거시 판정은 ensureSchemas가 소유)")
	}
}
