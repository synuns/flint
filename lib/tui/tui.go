// Package tui — the common wrappers for flint interactive TUI screens
// (bubbletea+huh). Screen models sit on top of these wrappers.
package tui

import (
	"errors"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/term"

	"github.com/synuns/flint/lib/wire"
)

// isTerminal — determines whether stdout is an interactive terminal (test-injectable).
// False for pipes, redirection, and CI. The old ModeCharDevice
// heuristic misdetected /dev/null as a terminal (it is also a char device — a
// stdout > /dev/null redirection triggered the TUI fallback exit 3). Replaced with
// term.IsTerminal(fd), an isatty-ioctl-family check that matches only terminals among char devices.
var isTerminal = func() bool {
	return term.IsTerminal(os.Stdout.Fd())
}

// noTUIEnvVar — the environment variable blocking TUI entry (the standard escape-hatch
// pattern, like GH_PROMPT_DISABLED). When set to a non-empty value ("1" recommended
// — the empty value counts as unset, the os.Getenv != "" convention), it blocks adaptive
// TUI entry entirely and forces the current path. The means for agents and CI to
// explicitly declare non-interactivity — the top layer of the 3-layer defense (environment variable -> isTerminal -> flag detection).
const noTUIEnvVar = "FLINT_NO_TUI"

// NoTUIForced — whether FLINT_NO_TUI is set (to a non-empty value). Adaptive-entry
// branching checks this before the TTY seam at all times (see each cli entry branch).
func NoTUIForced() bool {
	return os.Getenv(noTUIEnvVar) != ""
}

// IsTerminalStdout — the public wrapper of the stdout terminal check. The
// decision source of the adaptive entry branches (pending, export, report with no
// args + TTY -> TUI) — cli commands wrap this function in an injectable seam var
// for testing. Environment check first: with FLINT_NO_TUI set, false even on a terminal.
func IsTerminalStdout() bool { return !NoTUIForced() && isTerminal() }

// nonInteractiveHint — the adaptive-switch guidance: 1 line on how to escape
// non-interactively. Shown at the bottom of each TUI screen (for export, in the huh
// form description) — since no-arg runs now flow into the TUI, it announces that the pipe and flag paths still exist.
const nonInteractiveHint = "pipe output or pass flags for non-interactive mode"

// lowHeightThreshold — the low-screen (chrome-minimized) threshold (a shared
// constant): when the window height is below this value, the screen omits the
// optional chrome (non-interactive notice, separator blanks, status) from View and
// the list allocation formula reflects the reduced chrome rows. The q/ESC exit hint is kept even on low screens.
const lowHeightThreshold = 12

// lowHeight — the low-screen (chrome-minimized) branch verdict. A window size
// not yet received (0) is the pre-resize initial render — keep the existing layout.
func lowHeight(h int) bool { return h > 0 && h < lowHeightThreshold }

// Run — the bubbletea program execution wrapper. If stdout is not a terminal,
// it returns an error immediately before the program starts — the caller
// receives it and falls back to exit 3 (the flag-mode guidance). The ESC/q exit
// convention is followed by each screen model.
// ErrTerminalRequired — forcing the TUI on a non-terminal (--tui) is an invalid argument
// — the caller exit-3 is unchanged, and the ErrLine classification is the usage type
// ("commands left without the usage type: 0" — closing the residual gap of that contract;
// the internal report is abolished). Being a sentinel, errors.Is resolution is also preserved.
var ErrTerminalRequired = errors.New("TUI requires a terminal — use flag-mode instead")

// Run — the bubbletea program execution wrapper. If stdout is not a terminal,
// it returns an error immediately before the program starts — the caller
// receives it and falls back to exit 3 (the flag-mode guidance). The ESC/q exit
// convention is followed by each screen model.
func Run(model tea.Model) error {
	if !isTerminal() {
		return wire.UsageErrorf("%w", ErrTerminalRequired)
	}
	p := tea.NewProgram(model)
	_, err := p.Run()
	return err
}
