package tui

import (
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestThemeValuesExist(t *testing.T) {
	cases := []struct {
		name  string
		color lipgloss.TerminalColor
	}{
		{"Accent", Theme.Accent.GetForeground()},
		{"Secondary", Theme.Secondary.GetForeground()},
		{"Neutral", Theme.Neutral.GetForeground()},
	}
	for _, tc := range cases {
		if tc.color == nil {
			t.Errorf("Theme.%s: foreground color not set", tc.name)
		}
	}
}
