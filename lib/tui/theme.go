package tui

import "github.com/charmbracelet/lipgloss"

var Theme = struct {
	Accent    lipgloss.Style
	Secondary lipgloss.Style
	Neutral   lipgloss.Style
}{

	Accent: lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("4")),

	Secondary: lipgloss.NewStyle().Foreground(lipgloss.Color("6")),

	Neutral: lipgloss.NewStyle().Foreground(lipgloss.Color("8")),
}
