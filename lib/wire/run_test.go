// run_test.go — v1.5.3 T2 P2: ErrLine usage 분류 계약. 무효 인자
// (unknown flag·값 누락 등 사용법 오류)는 error:"usage"(값 확장 — 구조
// 불변, 하류 파서 호환), 내부 결함은 internal 유지, SchemaError는 schema
// 유지(회귀).
package wire

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/store"
)

// decodeErrLine — ErrLine 한 줄 JSON을 map으로 디코드.
func decodeErrLine(t *testing.T, line string) map[string]string {
	t.Helper()
	var m map[string]string
	if err := json.Unmarshal([]byte(strings.TrimSpace(line)), &m); err != nil {
		t.Fatalf("ErrLine JSON 디코드 실패: %v (%q)", err, line)
	}
	return m
}

// TestErrLineUsage — Review Focus(v1.5.3 P2): usage 오류의 error 값은
// "usage"(기존 internal에서 값만 교체).
func TestErrLineUsage(t *testing.T) {
	t.Run("usage 오류 — error:usage", func(t *testing.T) {
		m := decodeErrLine(t, ErrLine(UsageErrorf("unknown flag: %s", "--wat")))
		if m["error"] != "usage" {
			t.Errorf("error = %q, want usage", m["error"])
		}
		if !strings.Contains(m["message"], "unknown flag: --wat") {
			t.Errorf("message = %q, want 원 메시지 보존", m["message"])
		}
	})
	t.Run("내부 결함 — internal 유지", func(t *testing.T) {
		m := decodeErrLine(t, ErrLine(errors.New("boom")))
		if m["error"] != "internal" {
			t.Errorf("error = %q, want internal(내부 결함은 유지)", m["error"])
		}
	})
	t.Run("SchemaError — schema 유지(회귀)", func(t *testing.T) {
		m := decodeErrLine(t, ErrLine(&store.SchemaError{Field: "id", Expect: "integer"}))
		if m["error"] != "schema" {
			t.Errorf("error = %q, want schema", m["error"])
		}
	})
}

// TestUsageErrorfWrap — UsageErrorf는 %w 래핑을 지원하고 errors.Is로
// 원본 판정이 가능하다(UsageError는 분류 마커 — 원본 보존).
func TestUsageErrorfWrap(t *testing.T) {
	inner := errors.New("bad arg")
	ue := UsageErrorf("wrap: %w", inner)
	if !errors.Is(ue, inner) {
		t.Errorf("errors.Is(UsageError, 원본) = false, want true")
	}
	if ue.Error() != "wrap: bad arg" {
		t.Errorf("Error() = %q, want wrap: bad arg", ue.Error())
	}
}
