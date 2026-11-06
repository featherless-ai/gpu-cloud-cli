package output

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
)

// A terminal that reports no width (a pty without a size, as under some CI
// runners and docker exec) used to crash any prompt with a placeholder.
func TestPromptsDrawOnATerminalThatReportsNoWidth(t *testing.T) {
	form := newForm(huh.NewInput().Title("API key").Placeholder("flkey_…"), 0)
	form.Init()
	model, _ := form.Update(tea.WindowSizeMsg{Width: 0, Height: 24})

	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("drawing the prompt panicked: %v", recovered)
		}
	}()

	model.View()
}
