// setup.go — v1.5.0 T6: `flint setup` 위저드(huh 폼). exposure 선택 →
// 백업 키 안내를 수집만 하고, 실행은 cli의 기존 setup 로직이 담당한다
// (위저드는 플래그 등가물 조립 — 중복 구현 금지 계약).
package tui

import (
	"errors"
	"fmt"

	"github.com/charmbracelet/huh"
)

// 위저드 오류 계약 — 호출부(cli)는 두 오류 모두 기존 경로 폴백으로
// 처리한다(비터미널·취소 모두 부작용 없음).
var (
	// ErrWizardUnavailable — 터미널이 아니어서 위저드를 시작할 수 없음.
	ErrWizardUnavailable = errors.New("TUI requires a terminal — use flag-mode instead")
	// ErrWizardCancelled — 사용자가 ctrl+c로 위저드를 취소함.
	ErrWizardCancelled = errors.New("setup wizard cancelled")
)

// SetupAnswers — 위저드가 수집한 플래그 등가물.
type SetupAnswers struct {
	// Exposure — setup --exposure 값("normal"|"minimal").
	Exposure string
}

// BackupKeyNote — 백업 키 안내(기존 setup·RUNBOOK 백업 암호화 안내의
// 영문 표기: 옵트인·첫 암호화 시 자동 생성·분실 시 해독 불가).
const BackupKeyNote = `Encrypted backups are opt-in: "flint tuning set backup_encrypt true" or "flint backup --out <path> --encrypt".
The key file (backup.key, 0600) is created automatically on first encryption under FLINT_HOME.
If the key is lost, encrypted backups cannot be decrypted — keep a copy in a password manager.`

// runSetupFormFn — huh 폼 실행 seam(테스트 교체).
var runSetupFormFn = runSetupForm

// runSetupForm — 실제 huh 폼: ①exposure 선택 ②백업 키 안내.
// 완료 요약 화면은 cli가 기존 체인 결과·다음 단계 안내로 제공한다.
func runSetupForm() (SetupAnswers, error) {
	a := SetupAnswers{}
	err := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Exposure profile").
				Description("How much detail flint prints in command output.").
				Options(
					huh.NewOption("Normal — full detail", "normal"),
					huh.NewOption("Minimal — compact output", "minimal"),
				).
				Value(&a.Exposure),
		),
		huh.NewGroup(
			huh.NewNote().
				Title("Backup key").
				Description(BackupKeyNote),
		),
	).Run()
	return a, err
}

// RunSetupWizard — setup 위저드 실행. 비터미널은 폼 시작 전
// ErrWizardUnavailable, 사용자 취소(huh.ErrUserAborted)는
// ErrWizardCancelled로 변환한다. 허용값 밖 exposure는 선제 거부
// (setup --exposure 무효값 exit 3 계약과 정합).
func RunSetupWizard() (SetupAnswers, error) {
	if !isTerminal() {
		return SetupAnswers{}, ErrWizardUnavailable
	}
	a, err := runSetupFormFn()
	if err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return SetupAnswers{}, ErrWizardCancelled
		}
		return SetupAnswers{}, err
	}
	if a.Exposure != "normal" && a.Exposure != "minimal" {
		return SetupAnswers{}, fmt.Errorf("setup wizard: invalid exposure %q (want normal|minimal)", a.Exposure)
	}
	return a, nil
}
