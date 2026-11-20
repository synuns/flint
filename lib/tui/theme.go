// theme.go — the minimal TUI color theme (Lip Gloss).
package tui

import "github.com/charmbracelet/lipgloss"

// Theme — a minimal set: 1 accent, 1 secondary, 1 neutral. Instead of hard colors
// (#RRGGBB), only ANSI base-color numbers are used, respecting the terminal theme (the 16-color palette mapping).
var Theme = struct {
	Accent    lipgloss.Style
	Secondary lipgloss.Style
	Neutral   lipgloss.Style
}{
	// Accent — emphasis (titles, selected items): blue + bold.
	Accent: lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("4")),
	// Secondary — auxiliary (hints, key guidance): cyan.
	Secondary: lipgloss.NewStyle().Foreground(lipgloss.Color("6")),
	// Neutral — neutral (separators, meta info): light gray.
	Neutral: lipgloss.NewStyle().Foreground(lipgloss.Color("8")),
}
