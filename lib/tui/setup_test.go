// setup_test.go — setup 위저드: 비터미널 폴백·폼 seam·취소
// 계약·플래그 등가물 검증. 폼 본체(huh)는 runSetupFormFn seam으로 갈아
// 끼워 테스트한다(대화형 폼은 무인 테스트에서 실행 불가).
package tui

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/charmbracelet/huh"
)

// withFakeForm — runSetupFormFn seam 교체. SUPERLITE_REAL=1 seam 점검
// 모드에서는 실 huh 폼을 유지한다(대화형 — 폼을 호출하는 테스트는 스킵).
func withFakeForm(t *testing.T, fn func() (SetupAnswers, error)) {
	t.Helper()
	prev := runSetupFormFn
	runSetupFormFn = fn
	t.Cleanup(func() { runSetupFormFn = prev })
}

// requireFakeForm — seam 점검(실구현) 모드에서 폼 호출 테스트를 스킵.
func requireFakeForm(t *testing.T) {
	t.Helper()
	if os.Getenv("SUPERLITE_REAL") == "1" {
		t.Skip("real huh form is interactive — fake-form seam only")
	}
}

// withTerminal — isTerminal seam 교체(var 관례 승계).
func withTerminal(t *testing.T, v bool) {
	t.Helper()
	prev := isTerminal
	isTerminal = func() bool { return v }
	t.Cleanup(func() { isTerminal = prev })
}

// TestRunSetupWizardNonTerminal — 비터미널은 폼을 시작하지 않고 즉시
// ErrWizardUnavailable(호출부 cli는 기존 안내 경로로 폴백 — 계약).
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

// TestRunSetupWizardReturnsAnswers — 폼 완료 값(exposure) 전달.
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

// TestRunSetupWizardCancel — 사용자 취소(huh.ErrUserAborted)는
// ErrWizardCancelled로 변환(cli가 기존 경로 폴백 판별용).
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

// TestRunSetupWizardFormError — 취소 외 폼 오류는 원본 그대로 전파.
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

// TestRunSetupWizardInvalidExposure — seam이 플래그 허용값 외 값을 주면
// 거부(setup --exposure 무효값 exit 3 계약과 정합되게 위저드 선제 차단).
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

// TestBackupKeyNoteContent — 백업 키 안내는 기존 안내(RUNBOOK §백업
// 암호화)의 핵심 사실을 영문으로 유지한다: 옵트인·자동 생성·분실 시
// 해독 불가.
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

// containsFold — 대소문자 무시 부분문자열 판정(테스트 전용 헬퍼).
func containsFold(s, sub string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(sub))
}
