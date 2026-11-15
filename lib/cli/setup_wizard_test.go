// setup_wizard_test.go — v1.5.0 T6 진입 분기: 인자 없음+터미널 → 위저드
// seam 호출, 취소·비터미널 → 기존 no-arg 경로, 플래그(--exposure X)는
// 위저드와 무관하게 완전 불변(동치 회귀). 위저드 결과는 기존
// runSetupExposure·체인 재사용(중복 구현 금지 계약).
package cli

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
)

// withSetupWizard — setupWizardFn seam 교체(호출 수 반환).
func withSetupWizard(t *testing.T, chosen string, ok bool) *int {
	t.Helper()
	calls := 0
	prev := setupWizardFn
	setupWizardFn = func() (string, bool) {
		calls++
		return chosen, ok
	}
	t.Cleanup(func() { setupWizardFn = prev })
	return &calls
}

// withWizardTTY — setupWizardTTYFn seam 교체(위저드 진입 판정).
func withWizardTTY(t *testing.T, v bool) {
	t.Helper()
	prev := setupWizardTTYFn
	setupWizardTTYFn = func(io.Writer) bool { return v }
	t.Cleanup(func() { setupWizardTTYFn = prev })
}

// newExposureRecorder — meta upsert(exposure 기록) 포착 fakeStore.
func newExposureRecorder() (*fakeStore, *string, *string) {
	var key, val string
	st := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "INTO meta") && len(args) == 2 {
			key, val = args[0].(string), args[1].(string)
		}
		return nil, nil
	}}
	return st, &key, &val
}

// TestSetupWizardEntryBranch — 인자 없음+터미널: 위저드 완료 시 기존 체인
// 실행 후 위저드가 조립한 플래그 등가물(--exposure chosen)을 기존
// runSetupExposure로 기록한다.
func TestSetupWizardEntryBranch(t *testing.T) {
	st, key, val := newExposureRecorder()
	rec := withFakeCompose(t)
	calls := withSetupWizard(t, "minimal", true)
	withWizardTTY(t, true)
	var out bytes.Buffer
	code := runSetup(context.Background(), st, nil, strings.NewReader(""), &out)
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out.String())
	}
	if *calls != 1 {
		t.Fatalf("위저드 seam 호출=%d회, want 1", *calls)
	}
	if rec.upCalls != 1 {
		t.Fatalf("compose=%d회, want 1(기존 체인 재사용)", rec.upCalls)
	}
	if *key != ExposureKey || *val != "minimal" {
		t.Fatalf("기록=(%q,%q), want (%q,minimal)", *key, *val, ExposureKey)
	}
	if !strings.Contains(out.String(), `"exposure":"minimal"`) {
		t.Fatalf("완료 요약에 exposure 보고 부재:\n%s", out.String())
	}
}

// TestSetupWizardCancelFallsBack — 위저드 취소(("", false))는 기존
// no-arg 경로 그대로: 체인은 실행하되 exposure 기록은 없다.
func TestSetupWizardCancelFallsBack(t *testing.T) {
	st, key, val := newExposureRecorder()
	withFakeCompose(t)
	withSetupWizard(t, "", false)
	withWizardTTY(t, true)
	var out bytes.Buffer
	code := runSetup(context.Background(), st, nil, strings.NewReader(""), &out)
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out.String())
	}
	if *key != "" {
		t.Fatalf("취소 시 exposure 기록 발생: (%q,%q)", *key, *val)
	}
	if strings.Contains(out.String(), `"exposure"`) {
		t.Fatalf("취소 시 exposure 보고 출력:\n%s", out.String())
	}
	if !strings.Contains(out.String(), `"status"`) {
		t.Fatalf("기존 요약 JSON 부재:\n%s", out.String())
	}
}

// TestSetupNonTTYNoWizard — 비터미널(파이프·리다이렉션)에서는 위저드 seam을
// 부르지 않고 기존 안내 경로(한 줄 JSON 요약)를 유지한다.
func TestSetupNonTTYNoWizard(t *testing.T) {
	st, key, _ := newExposureRecorder()
	withFakeCompose(t)
	calls := withSetupWizard(t, "minimal", true) // seam이 ok여도 진입 불가
	var out bytes.Buffer
	code := runSetup(context.Background(), st, nil, strings.NewReader(""), &out)
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out.String())
	}
	if *calls != 0 {
		t.Fatalf("비터미널 위저드 호출=%d회, want 0", *calls)
	}
	if *key != "" {
		t.Fatalf("비터미널 exposure 기록 발생: %q", *key)
	}
	if strings.Count(strings.TrimSpace(out.String()), "\n") != 0 {
		t.Fatalf("기존 요약은 한 줄 JSON이어야 한다: %q", out.String())
	}
}

// TestSetupExposureFlagSkipsWizard — 플래그 경로 완전 불변: 터미널로
// 간주해도 --exposure X는 위저드를 거치지 않고 기존 단독 모드 결과와
// 동일하다(동치 회귀).
func TestSetupExposureFlagSkipsWizard(t *testing.T) {
	st, key, val := newExposureRecorder()
	rec := withFakeCompose(t)
	calls := withSetupWizard(t, "minimal", true)
	withWizardTTY(t, true) // 터미널로 간주해도 플래그 우선
	var out bytes.Buffer
	code := runSetup(context.Background(), st, []string{"--exposure", "minimal"}, strings.NewReader(""), &out)
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out.String())
	}
	if *calls != 0 {
		t.Fatalf("플래그 경로에서 위저드 호출=%d회, want 0(불변)", *calls)
	}
	if *key != ExposureKey || *val != "minimal" {
		t.Fatalf("기록=(%q,%q), want (%q,minimal)", *key, *val, ExposureKey)
	}
	// 기존 단독 모드 출력 동치 — exposure 보고 한 줄뿐, 체인 미실행.
	if got := strings.TrimSpace(out.String()); got != `{"status":"ok","exposure":"minimal"}` {
		t.Fatalf("출력=%q, want 기존 플래그 모드와 동일", got)
	}
	if rec.upCalls != 0 {
		t.Fatalf("플래그 단독 모드에 compose 실행=%d회, want 0(불변)", rec.upCalls)
	}
}
