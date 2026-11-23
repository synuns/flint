package tui

import (
	"errors"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/term"

	"github.com/synuns/flint/lib/wire"
)

var isTerminal = func() bool {
	return term.IsTerminal(os.Stdout.Fd())
}

const noTUIEnvVar = "FLINT_NO_TUI"

func NoTUIForced() bool {
	return os.Getenv(noTUIEnvVar) != ""
}

func IsTerminalStdout() bool { return !NoTUIForced() && isTerminal() }

const nonInteractiveHint = "pipe output or pass flags for non-interactive mode"

const lowHeightThreshold = 12

func lowHeight(h int) bool { return h > 0 && h < lowHeightThreshold }

var ErrTerminalRequired = errors.New("TUI requires a terminal — use flag-mode instead")

func Run(model tea.Model) error {
	if !isTerminal() {
		return wire.UsageErrorf("%w", ErrTerminalRequired)
	}
	p := tea.NewProgram(model)
	_, err := p.Run()
	return err
}
