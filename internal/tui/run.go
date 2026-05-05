package tui

import (
	"io"

	"github.com/S-Nakamur-a/gitplay/internal/model"
	"github.com/S-Nakamur-a/gitplay/internal/output"
)

// Run is the historical entrypoint kept for any internal callers. The
// CLI reaches the TUI via the output.Renderer registry.
func Run(h model.History) error {
	return runProgram(h)
}

// renderer adapts the TUI to the output.Renderer interface.
type renderer struct{}

func (renderer) Run(h model.History, _ output.Config, _ io.Writer) error {
	return runProgram(h)
}

func init() {
	output.Register("tui", renderer{})
}
