package tui

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/charmbracelet/huh"
)

func withFakeForm(t *testing.T, fn func() (SetupAnswers, error)) {
	t.Helper()
	prev := runSetupFormFn
	runSetupFormFn = fn
	t.Cleanup(func() { runSetupFormFn = prev })
}

func requireFakeForm(t *testing.T) {
	t.Helper()
	if os.Getenv("SUPERLITE_REAL") == "1" {
		t.Skip("real huh form is interactive — fake-form seam only")
	}
}

func withTerminal(t *testing.T, v bool) {
	t.Helper()
	prev := isTerminal
	isTerminal = func() bool { return v }
	t.Cleanup(func() { isTerminal = prev })
}

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

func containsFold(s, sub string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(sub))
}
