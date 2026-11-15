// theme.go — TUI 최소 색상 테마(Lip Gloss). v1.5.0 T5
package tui

import "github.com/charmbracelet/lipgloss"

// Theme — 강조 1·보조 1·중립 최소 색상 세트. 하드컬러(#RRGGBB) 대신
// ANSI 기본색 번호만 써서 터미널 테마(16색 팔레트 매핑)를 존중한다.
var Theme = struct {
	Accent    lipgloss.Style
	Secondary lipgloss.Style
	Neutral   lipgloss.Style
}{
	// Accent — 강조(제목·선택 항목): 파랑 + 볼드.
	Accent: lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("4")),
	// Secondary — 보조(힌트·키 안내): 시안.
	Secondary: lipgloss.NewStyle().Foreground(lipgloss.Color("6")),
	// Neutral — 중립(구분선·메타 정보): 밝은 회색.
	Neutral: lipgloss.NewStyle().Foreground(lipgloss.Color("8")),
}
