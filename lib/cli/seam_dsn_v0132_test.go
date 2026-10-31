// seam_dsn_v0132_test.go — 심층 리뷰 N1 회귀: SUPERLITE_REAL=1 실구현 seam의
// DSN 해결은 FLINT_TEST_DSN 설정 시만 실DB를 허용한다. 미설정이면 운영 기본
// DSN(5434/flint)으로 폴백하지 않고 fake 경로 — 프로브 실행의 운영 지표
// 오염(접속·쓰기)을 원천 차단한다. 기존 테스트 수정 금지 룰에 따라 신규 파일.
package cli

import (
	"context"
	"strings"
	"testing"
)

// TestSeamRealDSNGate — FLINT_TEST_DSN·FLINT_DSN 무설정 + SUPERLITE_REAL=1이면
// seamRealDSN은 ("", false) — dsnFromEnv 기본값(운영) 폴밭 없이 fake 폴백.
// 설정 시에만 (dsn, true)를 돌려준다.
func TestSeamRealDSNGate(t *testing.T) {
	t.Setenv("SUPERLITE_REAL", "1")
	t.Setenv("FLINT_TEST_DSN", "")
	t.Setenv("FLINT_DSN", "")
	dsn, real := seamRealDSN()
	if real || dsn != "" {
		t.Fatalf("seamRealDSN = (%q, %v), want (\"\", false) — 미설정 시 fake 폴백", dsn, real)
	}
	// withFake는 실구현 위임(delegatingFake)이 아니라 순수 fake를 설치한다
	// — 접속 시도 부재의 동작 단언(실구현 경로였다면 스킵 또는 위임 fake).
	f := &fakeStore{}
	withFake(t, f)
	st, err := newStore(context.Background(), "unused")
	if err != nil {
		t.Fatalf("newStore(fake): %v", err)
	}
	if got, ok := st.(*fakeStore); !ok || got != f {
		t.Fatalf("설치된 store = %T, want *fakeStore — 실구현 경로로 동작함", st)
	}

	// 설정 시 실DB 허용 — seamRealDSN은 DSN 해석만 하고 접속하지 않는다.
	const want = "postgresql://flint:t@h:5434/flint_ci"
	t.Setenv("FLINT_TEST_DSN", want)
	if dsn, real := seamRealDSN(); !real || dsn != want {
		t.Fatalf("seamRealDSN = (%q, %v), want (%q, true)", dsn, real, want)
	}
}

// TestSeamFakePathRunsWithoutRealDSN — 게이트 무설정 상태에서 withFake 경유
// 커맨드가 fake로 완결(삽입 기록) — 운영 접속·오염 없이 fast 경로 유지.
func TestSeamFakePathRunsWithoutRealDSN(t *testing.T) {
	t.Setenv("SUPERLITE_REAL", "1")
	t.Setenv("FLINT_TEST_DSN", "")
	t.Setenv("FLINT_DSN", "")
	f := &fakeStore{}
	withFake(t, f)
	out, code := mustRun(t, []string{"write", "knowledge"},
		`{"topic":"n1","summary":"seam gate","confidence":0.9}`)
	if code != 0 || !strings.Contains(out, `"status":"committed"`) {
		t.Fatalf("exit=%d out=%s — fake 경로가 깨짐", code, out)
	}
	if len(f.inserted) != 1 {
		t.Fatalf("fake 삽입 기록=%d건, want 1", len(f.inserted))
	}
}
