package tui

import (
	"fmt"
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

	// seedBase holds a TreeState with only the seed paths applied
	// (no commits stepped). nearestSnapshot Clones this when no
	// periodic snapshot is suitable, so backward jumps to the very
	// beginning still see the seeded "existing" set instead of an
	// empty tree. nil when no seed was provided.
	seedBase *replay.TreeState

	// addsAt[i] / removesAt[i] are the per-commit added / removed
	// line counts for commits[i]; filesAt[i] is the cumulative
	// unique-files count AFTER stepping commits[i]. Adds and removes
	// are kept separate (rather than summed into one "churn" number)
	// so the footer's bipolar churn chart can show "this commit was
	// mostly an add" vs "this commit was mostly a cleanup". All three
	// grow as batches arrive so the mini-graphs can render whatever
	// is loaded so far. Indexed parallel to history.Commits.
	addsAt    []int
	removesAt []int
	filesAt   []int

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

	// scramble configures the optional "movie decoder" typing
	// effect. Read by view_right.go each frame; the per-frame seed
	// comes from `frame`, which ticks once per Bubble Tea tick.
	scramble replay.ScrambleConfig
	frame    uint64

	// colorMode selects the timeline density encoding (see
	// ColorMode docstring above).
	colorMode ColorMode

	// viewMode selects what the left pane shows. Toggled by the
	// 't' key. Defaults to ViewModeTree (filesystem-style list);
	// ViewModeTreemap renders a squarified treemap weighted by
	// per-file LOC and shaded by current heat.
	viewMode ViewMode
}

// ViewMode selects the left-pane visualization.
//
//   - ViewModeTree (default): filesystem-style indented list, one
//     line per file, color = current heat tier. Best when the user
//     wants to read filenames and follow specific files.
//   - ViewModeTreemap: a squarified treemap weighted by per-file
//     LOC, colored by heat. Files-as-rectangles makes the
//     codebase's *shape* visible and turns the left pane into a
//     "subject" the camera can stay on across cuts.
type ViewMode int

const (
	ViewModeTree ViewMode = iota
	ViewModeTreemap
)

// Options carries optional behavioural toggles for the TUI entry
// points. Zero value preserves existing behaviour.
type Options struct {
	Scramble      bool
	ScrambleAhead int
	ColorMode     ColorMode

	// SeedPaths pre-populates the live filesystem view with paths
	// that already existed at the parent of the oldest loaded
	// commit. The TUI shows them as cold context rows so the user
	// sees the surrounding repo structure even with --max truncation.
	// Empty (nil) disables seeding — the tree will only contain
	// paths touched within the loaded window.
	SeedPaths []string
}

// ColorMode selects how the timeline density strip is shaded.
//
//   - ColorModeGradient (the default) holds the glyph fixed at "█"
//     and varies foreground brightness across a per-tag truecolor
//     ramp. Lipgloss snaps the ramp to the nearest available colors
//     on 256-color terminals (visible banding but still legible) and
//     loses fidelity on 16-color terminals — pick "glyph" there.
//   - ColorModeGlyph is the historical 5-glyph quartile encoding
//     (· ░ ▒ ▓ █). Coarser, but works identically on any terminal
//     and reads well against any background. Choose this when
//     running over a flaky SSH/tmux chain or in a 16-color shell.
type ColorMode int

const (
	ColorModeGradient ColorMode = iota
	ColorModeGlyph
)

// ParseColorMode maps the CLI flag value to a ColorMode. Empty and
// "gradient" both resolve to gradient (so an unset flag matches the
// CLI default). Returns an error rather than silently defaulting so
// typos surface immediately.
func ParseColorMode(s string) (ColorMode, error) {
	switch s {
	case "", "gradient":
		return ColorModeGradient, nil
	case "glyph":
		return ColorModeGlyph, nil
	default:
		return ColorModeGradient, fmt.Errorf("unknown color-mode %q (want one of: gradient, glyph)", s)
	}
}

func (o Options) scrambleConfig() replay.ScrambleConfig {
	if !o.Scramble {
		return replay.ScrambleConfig{}
	}
	ahead := o.ScrambleAhead
	if ahead <= 0 {
		ahead = replay.DefaultScrambleAhead
	}
	return replay.ScrambleConfig{Enabled: true, RevealAhead: ahead}
}

func newModel(h model.History, opts Options) programModel {
	var seedBase *replay.TreeState
	st := replay.NewTreeState(replay.DefaultHalfLife)
	if len(opts.SeedPaths) > 0 {
		st.Seed(opts.SeedPaths)
		seedBase = st.Clone()
	}
	if len(h.Commits) > 0 {
		st.Step(h.Commits[0])
	}
	m := programModel{
		history:   h,
		tree:      st,
		seedBase:  seedBase,
		idx:       0,
		playing:   true,
		playSpeed: 1.0,
		scramble:  opts.scrambleConfig(),
		colorMode: opts.ColorMode,
	}
	// Pre-compute the per-commit churn / cumulative-files arrays
	// for the footer sparklines. The streaming path populates these
	// incrementally in applyBatch; this branch covers Run(History).
	if n := len(h.Commits); n > 0 {
		m.commitDwell = m.computeDwell()
		m.addsAt = make([]int, n)
		m.removesAt = make([]int, n)
		m.filesAt = make([]int, n)
		walker := replay.NewTreeState(replay.DefaultHalfLife)
		for i, c := range h.Commits {
			adds, rems := 0, 0
			for _, f := range c.Files {
				adds += f.Added
				rems += f.Removed
			}
			m.addsAt[i] = adds
			m.removesAt[i] = rems
			walker.Step(c)
			m.filesAt[i] = walker.Counts().UniqueFiles
		}
	}
	return m
}

func newStreamingModel(branch, against string, ch <-chan gitlog.LoadBatch, opts Options) programModel {
	tree := replay.NewTreeState(replay.DefaultHalfLife)
	headTree := replay.NewTreeState(replay.DefaultHalfLife)
	var seedBase *replay.TreeState
	if len(opts.SeedPaths) > 0 {
		// Seed both trees: m.tree drives the user-visible pane,
		// m.headTree is the deepest-loaded snapshot used for cache
		// bucketing. They diverge only as the user scrubs back. The
		// seed-only Clone is held as seedBase so backward jumps that
		// fall before the first snapshot bucket can still rebuild
		// from the seed instead of starting empty.
		tree.Seed(opts.SeedPaths)
		headTree.Seed(opts.SeedPaths)
		seedBase = tree.Clone()
	}
	return programModel{
		history:   model.History{Branch: branch, Against: against},
		tree:      tree,
		headTree:  headTree,
		seedBase:  seedBase,
		loadCh:    ch,
		loading:   true,
		idx:       0,
		playing:   true,
		playSpeed: 1.0,
		scramble:  opts.scrambleConfig(),
		colorMode: opts.ColorMode,
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
