// Package terminalquery keeps Lip Gloss from asking the terminal for its
// background colour. Bubble Tea asks at start-up in every program that imports
// it, and a terminal that never answers holds each command up for 5 s.
//
// Go initializes ready packages in import path order, so this package, which
// imports only Lip Gloss, runs before github.com/charmbracelet/bubbletea.
package terminalquery

import "github.com/charmbracelet/lipgloss"

func init() {
	lipgloss.SetHasDarkBackground(true)
}
