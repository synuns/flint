// setup_wizard_test.go — entry branching: no args + terminal -> wizard
// seam call; cancel or non-terminal -> the existing no-arg path; flags (--exposure X) are
// fully unchanged regardless of the wizard (equivalence regression). The wizard result reuses the existing
// runSetupExposure and chain (the no-duplicate-implementation contract).
package cli

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
)

// withSetupWizard — swaps the setupWizardFn seam (returns the call count).
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

// withWizardTTY — swaps the setupWizardTTYFn seam (the wizard-entry decision).
func withWizardTTY(t *testing.T, v bool) {
	t.Helper()
	prev := setupWizardTTYFn
	setupWizardTTYFn = func(io.Writer) bool { return v }
	t.Cleanup(func() { setupWizardTTYFn = prev })
}

// newExposureRecorder — a fakeStore capturing meta upserts (the exposure write).
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

// TestSetupWizardEntryBranch — no args + terminal: on wizard completion, the existing chain
// runs, then the flag equivalent the wizard assembled (--exposure chosen) is recorded via the existing
// runSetupExposure.
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

// TestSetupWizardCancelFallsBack — wizard cancel (("", false)) keeps the existing
// no-arg path as-is: the chain runs but no exposure write.
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

// TestSetupNonTTYNoWizard — on non-terminal (pipe, redirection), the wizard seam is not
// called and the existing guidance path (one-line JSON summary) is kept.
func TestSetupNonTTYNoWizard(t *testing.T) {
	st, key, _ := newExposureRecorder()
	withFakeCompose(t)
	calls := withSetupWizard(t, "minimal", true) // even with the seam ok, no entry
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

// TestSetupExposureFlagSkipsWizard — the flag path is fully unchanged: even when treated as a
// terminal, --exposure X bypasses the wizard and matches the existing standalone-mode result
// (equivalence regression).
func TestSetupExposureFlagSkipsWizard(t *testing.T) {
	st, key, val := newExposureRecorder()
	rec := withFakeCompose(t)
	calls := withSetupWizard(t, "minimal", true)
	withWizardTTY(t, true) // even treated as a terminal, the flag wins
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
	// equivalent to the existing standalone-mode output — only the one exposure report line, no chain run.
	if got := strings.TrimSpace(out.String()); got != `{"status":"ok","exposure":"minimal"}` {
		t.Fatalf("출력=%q, want 기존 플래그 모드와 동일", got)
	}
	if rec.upCalls != 0 {
		t.Fatalf("플래그 단독 모드에 compose 실행=%d회, want 0(불변)", rec.upCalls)
	}
}
