// setup_test.go — the setup wizard: the non-terminal fallback, the form seam,
// the cancel contract, and the flag-equivalent verification. The form body (huh)
// is swapped via the runSetupFormFn seam for testing (an interactive form cannot
package tui

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/charmbracelet/huh"
)

// withFakeForm — swaps the runSetupFormFn seam. In the SUPERLITE_REAL=1
// seam-check mode, the real huh form is kept (interactive — form-calling tests skip).
func withFakeForm(t *testing.T, fn func() (SetupAnswers, error)) {
	t.Helper()
	prev := runSetupFormFn
	runSetupFormFn = fn
	t.Cleanup(func() { runSetupFormFn = prev })
}

// requireFakeForm — skips form-calling tests in the seam-check (real-implementation) mode.
func requireFakeForm(t *testing.T) {
	t.Helper()
	if os.Getenv("SUPERLITE_REAL") == "1" {
		t.Skip("real huh form is interactive — fake-form seam only")
	}
}

// withTerminal — swaps the isTerminal seam (inheriting the var convention).
func withTerminal(t *testing.T, v bool) {
	t.Helper()
	prev := isTerminal
	isTerminal = func() bool { return v }
	t.Cleanup(func() { isTerminal = prev })
}

// TestRunSetupWizardNonTerminal — non-terminal does not start the form and
// immediately gives ErrWizardUnavailable (the cli caller falls back to the existing guidance path — contract).
func TestRunSetupWizardNonTerminal(t *testing.T) {
	withTerminal(t, false)
	formCalled := false
	withFakeForm(t, func() (SetupAnswers, error) {
		formCalled = true
		return SetupAnswers{}, nil
	})
	_, err := RunSetupWizard()
	if err == nil {
		t.Fatal("비터미널 오류 없음 — 위저드가 시작되면 안 된다")
	}
	if !errors.Is(err, ErrWizardUnavailable) {
		t.Fatalf("err=%v, want ErrWizardUnavailable", err)
	}
	if formCalled {
		t.Error("비터미널에서 폼 실행됨")
	}
}

// TestRunSetupWizardReturnsAnswers — passes the completed form value (exposure) through.
func TestRunSetupWizardReturnsAnswers(t *testing.T) {
	requireFakeForm(t)
	withTerminal(t, true)
	withFakeForm(t, func() (SetupAnswers, error) {
		return SetupAnswers{Exposure: "minimal"}, nil
	})
	got, err := RunSetupWizard()
	if err != nil {
		t.Fatalf("err=%v, want nil", err)
	}
	if got.Exposure != "minimal" {
		t.Fatalf("exposure=%q, want minimal", got.Exposure)
	}
}

// TestRunSetupWizardCancel — a user cancel (huh.ErrUserAborted) is converted to
// ErrWizardCancelled (for cli's existing-path fallback detection).
func TestRunSetupWizardCancel(t *testing.T) {
	requireFakeForm(t)
	withTerminal(t, true)
	withFakeForm(t, func() (SetupAnswers, error) {
		return SetupAnswers{}, huh.ErrUserAborted
	})
	_, err := RunSetupWizard()
	if !errors.Is(err, ErrWizardCancelled) {
		t.Fatalf("err=%v, want ErrWizardCancelled", err)
	}
}

// TestRunSetupWizardFormError — form errors other than cancel propagate as-is.
func TestRunSetupWizardFormError(t *testing.T) {
	requireFakeForm(t)
	withTerminal(t, true)
	boom := errors.New("form boom")
	withFakeForm(t, func() (SetupAnswers, error) { return SetupAnswers{}, boom })
	_, err := RunSetupWizard()
	if !errors.Is(err, boom) {
		t.Fatalf("err=%v, want 전파(%v)", err, boom)
	}
	if errors.Is(err, ErrWizardCancelled) {
		t.Fatalf("일반 오류가 취소로 둔갑: %v", err)
	}
}

// TestRunSetupWizardInvalidExposure — if the seam supplies a value outside the
// flag's allowed set, it is rejected (the wizard's preemptive block, consistent with the setup --exposure invalid-value exit 3 contract).
func TestRunSetupWizardInvalidExposure(t *testing.T) {
	requireFakeForm(t)
	withTerminal(t, true)
	withFakeForm(t, func() (SetupAnswers, error) {
		return SetupAnswers{Exposure: "bogus"}, nil
	})
	_, err := RunSetupWizard()
	if err == nil || errors.Is(err, ErrWizardCancelled) {
		t.Fatalf("err=%v, want 무효값 오류(취소 아님)", err)
	}
}

// TestBackupKeyNoteContent — the backup-key notice keeps the key facts of the
// existing guidance (the RUNBOOK backup-encryption section) in English:
// opt-in, auto-generated, undecipherable if lost.
func TestBackupKeyNoteContent(t *testing.T) {
	for _, want := range []string{
		"backup_encrypt",
		"backup.key",
		"cannot be decrypted",
	} {
		if !containsFold(BackupKeyNote, want) {
			t.Errorf("백업 키 안내에 %q 부재:\n%s", want, BackupKeyNote)
		}
	}
}

// containsFold — a case-insensitive substring check (a test-only helper).
func containsFold(s, sub string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(sub))
}
