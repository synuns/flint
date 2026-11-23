package tui

import (
	"errors"
	"fmt"

	"github.com/charmbracelet/huh"
)

var (
	ErrWizardUnavailable = errors.New("TUI requires a terminal — use flag-mode instead")

	ErrWizardCancelled = errors.New("setup wizard cancelled")
)

type SetupAnswers struct {
	Exposure string
}

const BackupKeyNote = `Encrypted backups are opt-in: "flint tuning set backup_encrypt true" or "flint backup --out <path> --encrypt".
The key file (backup.key, 0600) is created automatically on first encryption under FLINT_HOME.
If the key is lost, encrypted backups cannot be decrypted — keep a copy in a password manager.`

var runSetupFormFn = runSetupForm

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
