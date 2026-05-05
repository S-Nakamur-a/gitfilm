package tui

import (
	"time"

	"github.com/S-Nakamur-a/gitfilm/internal/gitlog"
	"github.com/S-Nakamur-a/gitfilm/internal/model"
	"github.com/S-Nakamur-a/gitfilm/internal/replay"
	tea "github.com/charmbracelet/bubbletea"
)

// programModel is the Bubble Tea model for the TUI. All mutations
// happen on the event-loop goroutine via Update, so no locking is
// needed across loader/render boundaries.
type programModel struct {
	history model.History
	tree    *replay.TreeState

	// headTree mirrors the deepest loaded commit, regardless of
	// where the user has scrubbed. Snapshot bucketing follows
	// headTree (so backward-navigation cache stays correct as
	// commits stream in), while m.tree tracks the user's current
	// idx.
	headTree *replay.TreeState

	// snapshots[i] is a headTree clone taken AFTER stepping
	// commits[0..i*snapshotInterval]. Used by jumpTo to skip most
	// of the replay when navigating backwards.
	snapshots []*replay.TreeState

	// Streaming state. loadCh is nil when the program was given a
	// pre-built history; when non-nil, the model pulls commits in
	// batches and grows m.history over time.
	loadCh      <-chan gitlog.LoadBatch
	loadTotal   int
	loading     bool
	loadErr     error
	pausedAtEnd bool // set when autoplay hit the loaded end mid-stream

	idx          int // current commit index (0-based, oldest first)
	playing      bool
	dwellElapsed time.Duration
	commitDwell  time.Duration

	// playSpeed scales effective elapsed time relative to wall
	// time. 1.0 = baseline UnitsPerSecond.
	playSpeed float64

	width, height int
}

func newModel(h model.History) programModel {
	st := replay.NewTreeState(replay.DefaultHalfLife)
	if len(h.Commits) > 0 {
		st.Step(h.Commits[0])
	}
	m := programModel{
		history:   h,
		tree:      st,
		idx:       0,
		playing:   true,
		playSpeed: 1.0,
	}
	if len(h.Commits) > 0 {
		m.commitDwell = m.computeDwell()
	}
	return m
}

func newStreamingModel(branch, against string, ch <-chan gitlog.LoadBatch) programModel {
	return programModel{
		history:   model.History{Branch: branch, Against: against},
		tree:      replay.NewTreeState(replay.DefaultHalfLife),
		headTree:  replay.NewTreeState(replay.DefaultHalfLife),
		loadCh:    ch,
		loading:   true,
		idx:       0,
		playing:   true,
		playSpeed: 1.0,
	}
}

// ---- Bubble Tea wiring (msg types & helpers) ----

type tickMsg time.Time

func tick() tea.Cmd {
	return tea.Tick(frameTickMS, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// batchMsg / batchEndMsg drive the streaming-loader integration.
// Init kicks off a waitForBatch Cmd that resolves on the next
// channel send; Update processes the batch and (unless Done)
// re-arms the wait.
type batchMsg gitlog.LoadBatch
type batchEndMsg struct{}

func waitForBatch(ch <-chan gitlog.LoadBatch) tea.Cmd {
	return func() tea.Msg {
		b, ok := <-ch
		if !ok {
			return batchEndMsg{}
		}
		return batchMsg(b)
	}
}

func (m programModel) Init() tea.Cmd {
	if m.loadCh != nil {
		return tea.Batch(tick(), waitForBatch(m.loadCh))
	}
	return tick()
}
