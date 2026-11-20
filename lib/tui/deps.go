// deps.go — blank imports pinning the go.mod dependencies the TUI screens use.
// Importing them here keeps bubbles and huh as direct requires under go mod
// tidy. When implementing a screen, these imports may be replaced by actual use.
package tui

import (
	_ "github.com/charmbracelet/bubbles"
	_ "github.com/charmbracelet/huh"
)
