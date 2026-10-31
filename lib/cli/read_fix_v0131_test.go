// read_fix_v0131_test.go — 심층 리뷰 F1 회귀: --status 스펙 검증 강화.
// ①값 없는 trailing --status(단독·--as-of 병용 모두)는 값 없는 --as-of
// 가드와 동일 규칙으로 exit 3 — 조용히 무시 금지. ②무효 status 값 검증은
// --as-of 지정 여부와 무관하게 항상 수행(병용 시 as-of 우선 룰링은 유효한
// 값에만 적용). 기존 테스트 수정 금지 룰에 따라 신규 파일.
package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// TestReadStatusTrailingExits3 — trailing --status(값 없음)가 조용히 무시되지
// 않고 exit 3. --as-of 병용에서 무시가 조용히 성공하는 것이 본 결함이므로
// 두 변형 모두 커버한다. 검증은 질의 발행 전에 끝나야 한다.
func TestReadStatusTrailingExits3(t *testing.T) {
	for _, args := range [][]string{
		{"--status"},
		{"--as-of", "7d", "--status"},
	} {
		var out bytes.Buffer
		f := &fakeStore{queryFn: statusQueryFn()}
		if code := runRead(context.Background(), asofFakeOrReal(t, f), args, strings.NewReader(""), &out); code != 3 {
			t.Fatalf("args %v exit = %d, want 3 (out %q)", args, code, out.String())
		}
		if !strings.Contains(out.String(), `"field":"status"`) {
			t.Errorf("args %v status 스키마 에러 미출력: %q", args, out.String())
		}
		for _, q := range f.queries {
			t.Errorf("args %v 검증 전 질의 발행: %q", args, q)
		}
	}
}

// TestReadStatusInvalidWithAsOf — --as-of 병용 시에도 무효 status 값은
// exit 3으로 거부된다(기존에는 as-of 경로가 선행해 조용히 무시됨).
func TestReadStatusInvalidWithAsOf(t *testing.T) {
	var out bytes.Buffer
	fn := func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "dolt_log") {
			return asofQueryFn(sql, args)
		}
		return statusQueryFn()(sql, args)
	}
	f := &fakeStore{queryFn: fn}
	if code := runRead(context.Background(), asofFakeOrReal(t, f),
		[]string{"--as-of", "7d", "--status", "bogus"}, strings.NewReader(""), &out); code != 3 {
		t.Fatalf("exit = %d, want 3 (out %q)", code, out.String())
	}
	if !strings.Contains(out.String(), `"field":"status"`) {
		t.Errorf("status 스키마 에러 미출력: %q", out.String())
	}
	for _, q := range f.queries {
		t.Errorf("검증 전 질의 발행: %q", q)
	}
}
