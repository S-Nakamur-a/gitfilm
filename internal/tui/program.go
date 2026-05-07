// Package tui drives the Bubble Tea program that animates a git
// history. It splits across focused files so each concern stays
// readable on its own:
//
//   - layout.go     constants (sizes, pacing, snapshot interval)
//   - model.go      programModel struct + constructors + tea wiring
//   - update.go     Update reducer + key/tick/batch handlers
//   - nav.go        advance/jumpTo + snapshot cache
//   - pacing.go     dwell, progress, playSpeed
//   - view.go       View() top-level + chrome (header/subject/footer)
//   - view_tree.go  left-pane tree
//   - view_right.go right-pane commit & file cards (scroll-tail)
//   - view_timeline.go  bottom strip + per-commit progress bar
//   - style.go      module-level styles + heat/tag/status helpers
//   - util.go       clipPane / truncate / firstNonEmptyLine
package tui

import (
	"github.com/S-Nakamur-a/gitfilm/internal/gitlog"
	"github.com/S-Nakamur-a/gitfilm/internal/model"
	tea "github.com/charmbracelet/bubbletea"
)

// runProgram is the sync entry point: takes a fully loaded History and
// starts the Bubble Tea program.
func runProgram(h model.History, opts Options) error {
	m := newModel(h, opts)
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	_, err := p.Run()
	return err
}

// runStreamingProgram drives the TUI off a streaming loader. The
// channel is consumed inside Update via waitForBatch so all state
// mutations stay on the Bubble Tea event-loop goroutine.
func runStreamingProgram(branch, against string, ch <-chan gitlog.LoadBatch, opts Options) error {
	m := newStreamingModel(branch, against, ch, opts)
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	_, err := p.Run()
	return err
}
