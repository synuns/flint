// read_flag_v0132_test.go — 심층 리뷰 N9·N10 회귀: read 인자 파싱.
// N9: "--" 이후 전부 위치 인자(표준 end-of-options) — MCP 등 외부 입력의
// query가 "--json"처럼 보여도 플래그로 해석되지 않는다. N10: 값 없는
// trailing --tag/--entity/--domain/--fields는 F1의 --status/--as-of와 동일
// 규칙 exit 3. 기존 테스트 수정 금지 룰에 따라 신규 파일.
package cli

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
)

// TestReadDashDashTreatsRestAsQuery — N9: "--" 뒤 토큰은 검색어로 취급되고
// 출력 모드(텍스트)는 불변 — "--json"이 플래그로 파싱되지 않는다.
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
	// BuildRead는 q를 LIKE 패턴으로 감싸 인자화(%q%) — 인자에 "--json"이
	// 남아 있으면 검색어로 취급된 것(플래그 파싱이었다면 q가 비어 LIKE
	// 인자 자체가 없다).
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

// TestReadDashDashKeepsLeadingFlags — N9: "--" 앞 플래그는 그대로 파싱된다
// (출력 모드는 선행 --json이 지정한 대로 유지, 뒤 토큰은 검색어).
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

// TestReadTrailingFilterFlagsExit3 — N10: 값 없는 trailing
// --tag/--entity/--domain/--fields는 SchemaError exit 3 — 조용히 무시 시
// 의도와 다른 필터로 결과를 돌려주므로(--limit은 기존 exit 3).
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
