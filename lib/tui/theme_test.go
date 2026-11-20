// theme_test.go — asserts the minimal color-theme values exist.
package tui

import (
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// TestThemeValuesExist — the accent, secondary, and neutral styles must all
// have foreground colors (the color codes use ANSI base-color numbers so the terminal theme is respected).
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
