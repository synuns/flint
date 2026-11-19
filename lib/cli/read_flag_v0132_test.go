// read_flag_v0132_test.go — read argument parsing.
// Everything after "--" is positional (the standard end-of-options) — even if an external
// input's query (MCP etc.) looks like "--json", it is not parsed as a flag. A trailing
// --tag/--entity/--domain/--fields without a value follows the same rule as
// --status/--as-of: exit 3.
package cli

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
)

// TestReadDashDashTreatsRestAsQuery — N9: tokens after "--" are treated as the search query
// and the output mode (text) is unchanged — "--json" is not parsed as a flag.
func TestReadDashDashTreatsRestAsQuery(t *testing.T) {
	var gotArgs [][]any
	fn := func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM active_knowledge") {
			gotArgs = append(gotArgs, args)
		}
		return [][]any{{float64(1), "topic", "summary text"}}, nil
	}
	f := &fakeStore{queryFn: fn}
	var out bytes.Buffer
	if code := runRead(context.Background(), f, []string{"--", "--json"},
		strings.NewReader(""), &out); code != 0 {
		t.Fatalf("exit = %d, want 0 (out %q)", code, out.String())
	}
	// BuildRead wraps q into a LIKE pattern and parameterizes it (%q%) — if "--json" remains in the
	// arguments, it was treated as the query (under flag parsing, q would be empty and there would be no
	// LIKE argument at all).
	if len(gotArgs) == 0 || len(gotArgs[0]) < 2 ||
		!strings.Contains(fmt.Sprint(gotArgs[0][0]), "--json") {
		t.Fatalf("검색어 인자 = %v, want LIKE 인자에 \"--json\" — 플래그로 파싱됨", gotArgs)
	}
	if strings.HasPrefix(strings.TrimSpace(out.String()), "{") {
		t.Errorf("출력이 JSON 모드로 바뀜: %q — 모드 불변이어야 한다", out.String())
	}
	if !strings.Contains(out.String(), "1|topic|summary text") {
		t.Errorf("기본 텍스트 행 출력 없음: %q", out.String())
	}
}

// TestReadDashDashKeepsLeadingFlags — N9: flags before "--" are parsed as-is
// (the output mode stays as the leading --json specified; the trailing tokens are the query).
func TestReadDashDashKeepsLeadingFlags(t *testing.T) {
	fn := func(sql string, args []any) ([][]any, error) {
		return [][]any{{float64(1), "t", "s"}}, nil
	}
	f := &fakeStore{queryFn: fn}
	var out bytes.Buffer
	if code := runRead(context.Background(), f, []string{"--json", "--", "--json"},
		strings.NewReader(""), &out); code != 0 {
		t.Fatalf("exit = %d, want 0 (out %q)", code, out.String())
	}
	if !strings.HasPrefix(strings.TrimSpace(out.String()), "{") {
		t.Errorf("선행 --json 무시됨: %q — 모드 불변이어야 한다", out.String())
	}
}

// TestReadTrailingFilterFlagsExit3 — N10: a trailing --tag/--entity/--domain/--fields
// without a value is a SchemaError exit 3 — silently ignoring it would return results under
// different filters than intended (--limit already exits 3).
func TestReadTrailingFilterFlagsExit3(t *testing.T) {
	for _, flag := range []string{"--tag", "--entity", "--domain", "--fields"} {
		var out bytes.Buffer
		f := &fakeStore{}
		if code := runRead(context.Background(), f, []string{flag},
			strings.NewReader(""), &out); code != 3 {
			t.Fatalf("%s exit = %d, want 3 (out %q)", flag, code, out.String())
		}
		field := strings.TrimPrefix(flag, "--")
		if !strings.Contains(out.String(), `"field":"`+field+`"`) {
			t.Errorf("%s 스키마 에러 미출력: %q", flag, out.String())
		}
		if len(f.queries) != 0 {
			t.Errorf("%s 검증 전 질의 발행: %q", flag, f.queries[0])
		}
	}
}
