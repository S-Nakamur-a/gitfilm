package tui

import (
	"github.com/S-Nakamur-a/gitfilm/internal/gitlog"
	"github.com/S-Nakamur-a/gitfilm/internal/replay"
	tea "github.com/charmbracelet/bubbletea"
)

// Update is the Bubble Tea reducer. Branches are kept thin — each
// concern (key handling, autoplay tick, batch ingest) is a method.
func (m programModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		// Visible-card count depends on terminal height, and dwell
		// is sized to the largest visible file's budget — so a
		// resize must invalidate the previous dwell.
		if len(m.history.Commits) > 0 {
			m.commitDwell = m.computeDwell()
		}
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)

	case tickMsg:
		// frame ticks every Update tick regardless of playback so the
		// scramble shimmer keeps moving even when paused (otherwise
		// the noise visibly freezes mid-line).
		m.frame++
		if m.playing {
			m.dwellElapsed += frameTickMS
			if m.effectiveElapsed() >= m.commitDwell {
				m.advanceOrPauseAtEnd()
			}
		}
		return m, tick()

	case batchMsg:
		return m.applyBatch(gitlog.LoadBatch(msg))

	case batchEndMsg:
		// Loader closed without a Done batch (shouldn't happen by
		// protocol, but be defensive).
		m.loading = false
		return m, nil
	}
	return m, nil
}

func (m programModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c", "esc":
		return m, tea.Quit
	case " ", "p":
		m.playing = !m.playing
	case "right", "l", ".":
		m.advance(1)
	case "left", "h", ",":
		m.advance(-1)
	case "shift+right", "L":
		m.advance(10)
	case "shift+left", "H":
		m.advance(-10)
	case "g":
		m.jumpTo(0)
	case "G":
		m.jumpTo(len(m.history.Commits) - 1)
	case "+", "=", ">":
		m.bumpPlaySpeed(+1)
	case "-", "_", "<":
		m.bumpPlaySpeed(-1)
	case "0":
		m.playSpeed = 1.0
	case "t":
		// Toggle left-pane view: tree ↔ treemap. Pure render-side
		// switch — does not affect playback or state.
		if m.viewMode == ViewModeTree {
			m.viewMode = ViewModeTreemap
		} else {
			m.viewMode = ViewModeTree
		}
	case "down", "j":
		m.filesOffset = min(m.filesOffset+1, filesScrollCap(m))
	case "up", "k":
		m.filesOffset = max(0, m.filesOffset-1)
	case "pgdown", "ctrl+d":
		m.filesOffset = min(m.filesOffset+scrollPageStep(m.height), filesScrollCap(m))
	case "pgup", "ctrl+u":
		m.filesOffset = max(0, m.filesOffset-scrollPageStep(m.height))
	case "J":
		m.treeOffset = min(m.treeOffset+1, treeScrollCap(m))
	case "K":
		m.treeOffset = max(0, m.treeOffset-1)
	}
	return m, nil
}

// scrollPageStep returns the half-page jump used by pgup/pgdown. We
// derive it from terminal height instead of hardcoding so a tall
// terminal pages by more lines than a short one — the user's "one
// page" intuition stays intact across window sizes. Floor at 1 so a
// pathologically short window still scrolls.
func scrollPageStep(termHeight int) int {
	step := termHeight / 2
	if step < 1 {
		step = 1
	}
	return step
}

// filesScrollCap is a loose upper bound on right-pane offset growth.
// scrollWindow re-clamps to the actual content height at render
// time, but Bubble Tea's value-receiver View can't write the clamp
// back to the model, so without a handler-side cap a user holding
// pgdown past the bottom would balloon the offset and then need
// dozens of pgup presses before the window starts moving. The cap
// is `commitFiles * (max card height) + slack` so it stays comfortably
// above any plausible rendered total without being precise.
func filesScrollCap(m programModel) int {
	if m.idx < 0 || m.idx >= len(m.history.Commits) {
		return 0
	}
	files := len(m.history.Commits[m.idx].Files)
	return files*expandedCardLines + commitCardRows + 8
}

// treeScrollCap is the analog of filesScrollCap for the left pane.
// The tree's rendered row count is bounded by touched paths plus
// seeded paths plus directory rows; we approximate with
// touches+existing and add slack for directory rows and placeholder
// summaries.
func treeScrollCap(m programModel) int {
	if m.tree == nil {
		return 0
	}
	cnt := m.tree.Counts()
	return cnt.UniqueFiles + 32
}

// advanceOrPauseAtEnd is called when the dwell for the current
// commit has elapsed. Steps forward, or — at the end of the loaded
// range while still streaming — flags pausedAtEnd so the next batch
// wakes playback.
func (m *programModel) advanceOrPauseAtEnd() {
	if m.idx < len(m.history.Commits)-1 {
		m.advance(1)
		return
	}
	if m.loading {
		m.pausedAtEnd = true
	}
	m.playing = false
}

// applyBatch appends streamed commits, steps headTree, populates
// snapshot buckets, and resumes autoplay if it had paused at the
// previously-loaded end. Single-threaded relative to Update.
func (m programModel) applyBatch(b gitlog.LoadBatch) (tea.Model, tea.Cmd) {
	if b.Err != nil {
		m.loadErr = b.Err
		m.loading = false
		return m, nil
	}
	if b.Total > 0 {
		m.loadTotal = b.Total
	}
	if len(b.Commits) > 0 {
		startIdx := len(m.history.Commits)
		m.history.Commits = append(m.history.Commits, b.Commits...)
		if m.headTree == nil {
			m.headTree = replay.NewTreeState(replay.DefaultHalfLife)
		}
		for i := range b.Commits {
			c := b.Commits[i]
			adds, rems := 0, 0
			for _, f := range c.Files {
				adds += f.Added
				rems += f.Removed
			}
			m.addsAt = append(m.addsAt, adds)
			m.removesAt = append(m.removesAt, rems)
			m.headTree.Step(c)
			absIdx := startIdx + i
			if (absIdx+1)%snapshotInterval == 0 {
				m.recordSnapshot(absIdx, m.headTree)
			}
		}
		// First batch: bootstrap m.tree at idx=0 so the first
		// frame has something to render. Reuse the TreeState built
		// in newStreamingModel rather than allocating a new one —
		// any seed paths the loader pre-populated must survive into
		// the rendered tree.
		if startIdx == 0 && len(m.history.Commits) > 0 {
			m.tree.Step(m.history.Commits[0])
			m.commitDwell = m.computeDwell()
		}
		if m.pausedAtEnd {
			m.pausedAtEnd = false
			m.playing = true
			m.dwellElapsed = 0
		}
	}
	if b.Done {
		m.loading = false
		return m, nil
	}
	return m, waitForBatch(m.loadCh)
}
