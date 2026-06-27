// Package tui implements the interactive terminal UI for membox.
//
// The TUI is intentionally thin: it only handles input, display, completion,
// and history. All business logic is delegated to the app layer.
package tui

import (
	"github.com/earendil-works/membox/internal/app"
	tea "github.com/charmbracelet/bubbletea"
)

// Run starts the interactive membox TUI.
func Run() error {
	a, err := app.New()
	if err != nil {
		return err
	}
	defer a.Close()

	m := newModel(a)
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err = p.Run()
	return err
}
