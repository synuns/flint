// setup.go — the `flint setup` wizard (a huh form). It only collects the exposure
// selection and backup-key acknowledgment; execution is handled by cli's existing
// setup logic (the wizard assembles the flag equivalent — the no-duplicate-implementation contract).
package tui

import (
	"errors"
	"fmt"

	"github.com/charmbracelet/huh"
)

// the wizard error contracts — the caller (cli) handles both errors by falling
// back to the existing paths (both non-terminal and cancel are side-effect-free).
var (
	// ErrWizardUnavailable — not a terminal, so the wizard cannot start.
	ErrWizardUnavailable = errors.New("TUI requires a terminal — use flag-mode instead")
	// ErrWizardCancelled — the user cancelled the wizard with ctrl+c.
	ErrWizardCancelled = errors.New("setup wizard cancelled")
)

// SetupAnswers — the flag equivalents the wizard collected.
type SetupAnswers struct {
	// Exposure — the setup --exposure value ("normal"|"minimal").
	Exposure string
}

// BackupKeyNote — the backup-key notice (the English rendering of the existing
// setup/RUNBOOK backup-encryption notice: opt-in, auto-generated on first encryption, undecipherable if lost).
const BackupKeyNote = `Encrypted backups are opt-in: "flint tuning set backup_encrypt true" or "flint backup --out <path> --encrypt".
The key file (backup.key, 0600) is created automatically on first encryption under FLINT_HOME.
If the key is lost, encrypted backups cannot be decrypted — keep a copy in a password manager.`

// runSetupFormFn — the huh form execution seam (test swap).
var runSetupFormFn = runSetupForm

// runSetupForm — the real huh form: (1) exposure selection (2) the backup-key
// notice. The completion-summary screen is provided by cli from the existing chain's results and next-step guidance.
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

// RunSetupWizard — runs the setup wizard. Non-terminal gives
// ErrWizardUnavailable before the form starts; a user cancel
// (huh.ErrUserAborted) is converted to ErrWizardCancelled. An exposure outside
// the allowed values is preemptively rejected (consistent with the setup --exposure invalid-value exit 3 contract).
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
