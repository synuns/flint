// deps.go — TUI 화면이 사용할 의존의 go.mod 고정용 블랭크 임포트.
// 여기서 임포트해야 go mod tidy가 bubbles·huh를 직접
// require로 유지한다. 화면 구현 시 이 파일의 임포트를 실제 사용으로
// 대체해도 된다.
package tui

import (
	_ "github.com/charmbracelet/bubbles"
	_ "github.com/charmbracelet/huh"
)
