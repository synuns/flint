// theme_test.go — 최소 색상 테마 값 존재 단언. v1.5.0 T5
package tui

import (
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// TestThemeValuesExist — 강조·보조·중립 스타일이 모두 전경색을 가져야
// 한다(색 코드는 터미널 테마가 존중되는 ANSI 기본색 번호 사용).
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
