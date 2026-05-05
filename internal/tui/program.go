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
	"time"

	"github.com/S-Nakamur-a/gitfilm/internal/gitlog"
	"github.com/S-Nakamur-a/gitfilm/internal/model"
	"github.com/S-Nakamur-a/gitfilm/internal/replay"
	tea "github.com/charmbracelet/bubbletea"
)

// DwellFor is re-exported for the CLI's --stats path.
func DwellFor(c model.Commit) time.Duration { return replay.DwellFor(c) }

// runProgram is the legacy / sync entry point: takes a fully
// loaded History and starts the Bubble Tea program.
func runProgram(h model.History) error {
	m := newModel(h)
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	_, err := p.Run()
	return err
}

// runStreamingProgram drives the TUI off a streaming loader. The
// channel is consumed inside Update via waitForBatch so all state
// mutations stay on the Bubble Tea event-loop goroutine.
func runStreamingProgram(branch, against string, ch <-chan gitlog.LoadBatch) error {
	m := newStreamingModel(branch, against, ch)
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	_, err := p.Run()
	return err
}
