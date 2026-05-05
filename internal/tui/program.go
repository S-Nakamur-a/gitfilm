package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/S-Nakamur-a/gitfilm/internal/gitlog"
	"github.com/S-Nakamur-a/gitfilm/internal/model"
	"github.com/S-Nakamur-a/gitfilm/internal/replay"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// TUI-only tunables. Animation pacing and tree-state knobs live in
// internal/replay so the HTML renderer sees the same numbers.
var (
	frameTickMS = 50 * time.Millisecond
	// snapshotInterval controls TreeState caching for fast backward
	// navigation. Cache a snapshot every N commits so jumping back
	// only replays at most N commits instead of the full prefix.
	snapshotInterval = 100
)

// DwellFor is re-exported for the CLI's --stats path.
func DwellFor(c model.Commit) time.Duration { return replay.DwellFor(c) }

type tickMsg time.Time

func tick() tea.Cmd {
	return tea.Tick(frameTickMS, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// batchMsg / batchEndMsg drive the streaming-loader integration. Init
// kicks off a waitForBatch Cmd that resolves on the next channel send;
// Update processes the batch and (unless Done) re-arms the wait. This
// keeps the model single-threaded — no need to lock state across the
// loader goroutine and the TUI's event loop.
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

type programModel struct {
	history model.History
	tree    *replay.TreeState
	// headTree mirrors the deepest loaded commit, regardless of where
	// the user has scrubbed. Snapshot bucketing is driven by headTree
	// (so backward-navigation cache stays correct as commits stream
	// in), while m.tree continues to track the user's current idx.
	headTree *replay.TreeState
	// snapshots[i] is a headTree clone taken AFTER stepping through
	// commits[0..i*snapshotInterval]. Used to skip most of the replay
	// when jumping backwards on large histories.
	snapshots []*replay.TreeState

	// Streaming state. loadCh is nil when the program was given a
	// pre-built history (legacy/sync path); when non-nil, the model
	// pulls commits in batches and grows m.history over time.
	loadCh      <-chan gitlog.LoadBatch
	loadTotal   int
	loading     bool
	loadErr     error
	pausedAtEnd bool // set when autoplay hit the loaded end mid-stream

	idx          int // current commit index (0-based, oldest first)
	playing      bool
	dwellElapsed time.Duration
	commitDwell  time.Duration // total dwell time for the current commit
	// playSpeed scales both elapsed-time-vs-dwell pacing and the typing
	// units rate. 1.0 = baseline UnitsPerSecond.
	playSpeed float64

	width, height int
}

// playSpeedSteps is the discrete ladder used by the +/- keys. Includes
// 1.0 so the user can always restore the default cadence; otherwise
// arbitrary float arithmetic would let the speed drift off the
// calibrated point.
var playSpeedSteps = []float64{0.25, 0.5, 0.75, 1.0, 1.5, 2.0, 3.0, 4.0}

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

func (m programModel) Init() tea.Cmd {
	if m.loadCh != nil {
		return tea.Batch(tick(), waitForBatch(m.loadCh))
	}
	return tick()
}

func (m programModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		// Visible-card count depends on terminal height, and dwell is
		// sized to the largest *visible* file's budget — so a resize
		// must invalidate the previous dwell or playback feels off.
		if len(m.history.Commits) > 0 {
			m.commitDwell = m.computeDwell()
		}
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			return m, tea.Quit
		case " ", "p":
			m.playing = !m.playing
			return m, nil
		case "right", "l", ".":
			m.advance(1)
			return m, nil
		case "left", "h", ",":
			m.advance(-1)
			return m, nil
		case "shift+right", "L":
			m.advance(10)
			return m, nil
		case "shift+left", "H":
			m.advance(-10)
			return m, nil
		case "g":
			m.jumpTo(0)
			return m, nil
		case "G":
			m.jumpTo(len(m.history.Commits) - 1)
			return m, nil
		case "+", "=", ">":
			m.bumpPlaySpeed(+1)
			return m, nil
		case "-", "_", "<":
			m.bumpPlaySpeed(-1)
			return m, nil
		case "0":
			m.playSpeed = 1.0
			return m, nil
		}
	case tickMsg:
		if m.playing {
			m.dwellElapsed += frameTickMS
			// playSpeed accelerates wall-clock progress: a 2x speed
			// makes 1s of real time count as 2s of dwell. Symmetrically
			// scales the typing-units rate in renderRight.
			if m.effectiveElapsed() >= m.commitDwell {
				if m.idx < len(m.history.Commits)-1 {
					m.advance(1)
				} else {
					// End of LOADED range. If still streaming, mark for
					// auto-resume so the next batch pulls playback
					// forward without the user having to press space.
					if m.loading {
						m.pausedAtEnd = true
					}
					m.playing = false
				}
			}
		}
		return m, tick()
	case batchMsg:
		return m.applyBatch(gitlog.LoadBatch(msg))
	case batchEndMsg:
		// Loader closed the channel without a Done batch (shouldn't
		// happen by protocol, but be defensive).
		m.loading = false
		return m, nil
	}
	return m, nil
}

// applyBatch appends new commits, steps the head tree, populates
// snapshot buckets, and arranges autoplay to resume if it had paused
// at the previously-loaded end. Single-threaded relative to Update so
// no locking needed.
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
			m.headTree.Step(b.Commits[i])
			absIdx := startIdx + i
			// Snapshot bucket boundary — clone headTree so backward
			// navigation can rewind to here without replaying from 0.
			if (absIdx+1)%snapshotInterval == 0 {
				bucket := absIdx / snapshotInterval
				for len(m.snapshots) <= bucket {
					m.snapshots = append(m.snapshots, nil)
				}
				if m.snapshots[bucket] == nil {
					m.snapshots[bucket] = m.headTree.Clone()
				}
			}
		}
		// First batch: bootstrap m.tree at idx=0 so the first frame
		// has something to render.
		if startIdx == 0 && len(m.history.Commits) > 0 {
			m.tree = replay.NewTreeState(replay.DefaultHalfLife)
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

// advance moves idx by n (clamped) and replays the tree state.
// Backwards jumps use the periodic snapshot cache so we never replay
// more than `snapshotInterval` commits.
func (m *programModel) advance(n int) {
	target := m.idx + n
	if target < 0 {
		target = 0
	}
	if target >= len(m.history.Commits) {
		target = len(m.history.Commits) - 1
	}
	m.jumpTo(target)
}

func (m *programModel) jumpTo(target int) {
	if target == m.idx {
		return
	}
	if target < m.idx {
		// Find nearest snapshot at or before target.
		baseIdx, base := m.nearestSnapshot(target)
		st := base.Clone()
		for i := baseIdx + 1; i <= target; i++ {
			st.Step(m.history.Commits[i])
		}
		m.tree = st
	} else {
		// stepping forward from current state is already fast
		for i := m.idx + 1; i <= target; i++ {
			m.tree.Step(m.history.Commits[i])
			m.maybeSnapshot(i)
		}
	}
	m.idx = target
	m.dwellElapsed = 0
	if target >= 0 && target < len(m.history.Commits) {
		m.commitDwell = m.computeDwell()
	}
}

// effectiveElapsed scales raw dwellElapsed by playSpeed. Used for both
// the auto-advance check and the typing units rate so they stay in
// lockstep — a 2x speed runs the typing animation twice as fast AND
// shortens the wall-clock dwell by half.
func (m programModel) effectiveElapsed() time.Duration {
	return time.Duration(float64(m.dwellElapsed) * m.playSpeed)
}

// commitProgress returns the user-visible fraction (0..1) through the
// current commit's dwell, clamped. Drives the per-commit progress bar
// and the time-axis caret.
func (m programModel) commitProgress() float64 {
	if m.commitDwell <= 0 {
		return 0
	}
	f := float64(m.effectiveElapsed()) / float64(m.commitDwell)
	if f < 0 {
		f = 0
	}
	if f > 1 {
		f = 1
	}
	return f
}

// expandableCount mirrors the "how many full cards fit in the right
// pane" calc from renderRight, so dwell sizing and rendering agree on
// which files contribute. Falls back to a sensible default when the
// height isn't known yet (pre-WindowSizeMsg).
func (m programModel) expandableCount() int {
	h := m.height
	if h <= 0 {
		h = 30
	}
	// Footer (5 rows) + header (1) + subject (1) + pane borders (2) ≈ 9
	// rows of chrome around the right pane. Same constants the live
	// view uses, just approximated upfront so we don't need to render
	// to compute dwell.
	const chrome = 9
	bodyH := h - chrome
	if bodyH < 4 {
		bodyH = 4
	}
	available := bodyH - 5 // commit summary card consumes ~5 rows
	if available < 4 {
		available = 4
	}
	const expandedCardLines = 7
	expandable := available / expandedCardLines
	if expandable < 1 {
		expandable = 1
	}
	return expandable
}

// computeDwell sizes the dwell to the largest *visible* file's budget,
// not the largest budget in the commit. Previously a commit with one
// huge offscreen file (rendered as a one-line summary) would dwell for
// the full 3s clamp while the visible cards finished typing in a few
// hundred ms — leaving the user staring at a static screen most of the
// time.
//
// Adds a small read tail so the user can see the finished cards before
// the next commit replaces them.
func (m programModel) computeDwell() time.Duration {
	const readTail = 350 * time.Millisecond
	if m.idx < 0 || m.idx >= len(m.history.Commits) {
		return replay.MinCommitMS
	}
	c := m.history.Commits[m.idx]
	expandable := m.expandableCount()
	maxB := 0
	for i, f := range c.Files {
		if i >= expandable {
			break
		}
		if b := replay.FileBudget(f); b > maxB {
			maxB = b
		}
	}
	if maxB == 0 {
		maxB = replay.MinFileBudget
	}
	secs := float64(maxB) / replay.UnitsPerSecond
	d := time.Duration(secs*float64(time.Second)) + readTail
	if d < replay.MinCommitMS {
		d = replay.MinCommitMS
	}
	if d > replay.MaxCommitMS {
		d = replay.MaxCommitMS
	}
	return d
}

// bumpPlaySpeed steps along playSpeedSteps. Snaps to the nearest step
// first when the current speed isn't on the ladder (defensive — could
// only happen via persisted state someday).
func (m *programModel) bumpPlaySpeed(dir int) {
	cur := 0
	bestDiff := 1e9
	for i, s := range playSpeedSteps {
		d := s - m.playSpeed
		if d < 0 {
			d = -d
		}
		if d < bestDiff {
			bestDiff = d
			cur = i
		}
	}
	next := cur + dir
	if next < 0 {
		next = 0
	}
	if next >= len(playSpeedSteps) {
		next = len(playSpeedSteps) - 1
	}
	m.playSpeed = playSpeedSteps[next]
}

// nearestSnapshot returns the snapshot whose index is the largest one
// <= target, plus a base TreeState we can clone from. Falls back to a
// fresh empty TreeState (representing "before commit 0") with
// baseIdx = -1 when no snapshot is suitable.
func (m *programModel) nearestSnapshot(target int) (int, *replay.TreeState) {
	bucket := target / snapshotInterval
	if bucket < len(m.snapshots) && m.snapshots[bucket] != nil {
		return bucket*snapshotInterval + (snapshotInterval - 1), m.snapshots[bucket]
	}
	// scan downward for any earlier snapshot
	for b := bucket - 1; b >= 0; b-- {
		if b < len(m.snapshots) && m.snapshots[b] != nil {
			return b*snapshotInterval + (snapshotInterval - 1), m.snapshots[b]
		}
	}
	return -1, replay.NewTreeState(replay.DefaultHalfLife)
}

// maybeSnapshot records a TreeState clone after stepping through
// commit i, but only on bucket boundaries.
func (m *programModel) maybeSnapshot(i int) {
	if (i+1)%snapshotInterval != 0 {
		return
	}
	bucket := i / snapshotInterval
	for len(m.snapshots) <= bucket {
		m.snapshots = append(m.snapshots, nil)
	}
	if m.snapshots[bucket] == nil {
		m.snapshots[bucket] = m.tree.Clone()
	}
}

// ---- view ----

var (
	styleTitle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212"))
	styleSubject  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("231"))
	styleFilePath = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("117"))
	styleDim      = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	styleAdd      = lipgloss.NewStyle().Foreground(lipgloss.Color("46"))
	styleDel      = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	styleNew      = lipgloss.NewStyle().Foreground(lipgloss.Color("220")).Bold(true)
	styleGhost    = lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Faint(true)
	styleFeat     = lipgloss.NewStyle().Foreground(lipgloss.Color("213"))
	styleAgst     = lipgloss.NewStyle().Foreground(lipgloss.Color("75"))
	stylePane     = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1)
)

func (m programModel) View() string {
	if m.width == 0 {
		return "loading…"
	}
	if len(m.history.Commits) == 0 {
		if m.loadErr != nil {
			return styleDel.Render("load error: " + m.loadErr.Error())
		}
		if m.loadTotal > 0 {
			return fmt.Sprintf("loading commits… (0 / %d)", m.loadTotal)
		}
		return "loading commits…"
	}
	cur := m.history.Commits[m.idx]

	// Build the chrome first so we can subtract its actual rendered
	// height from the terminal size — guessing constants ("headerH=1")
	// caused the top of the screen to clip when WindowSizeMsg lied
	// or when the subject wrapped to two lines.
	header := clipPane(m.renderHeader(cur), m.width, 1)
	subject := clipPane(m.renderSubject(cur, m.width-2), m.width, 1)
	footer := m.renderFooter()
	chromeH := lipgloss.Height(header) + lipgloss.Height(subject) + lipgloss.Height(footer)

	bodyH := m.height - chromeH
	if bodyH < 6 {
		bodyH = 6
	}
	// Width split: aim for 2/5 left, but keep both panes ≥ their
	// minimums *and* leftW + rightW == m.width so JoinHorizontal never
	// produces lines wider than the terminal. Letting the floors stack
	// (left=28, right=30 on a 40-cell terminal) overflowed and wrapped,
	// which is what the user saw as "崩れる" on resize.
	const minLeft, minRight = 28, 30
	leftW := m.width * 2 / 5
	if leftW < minLeft {
		leftW = minLeft
	}
	if m.width-leftW < minRight {
		leftW = m.width - minRight
	}
	if leftW < 1 {
		leftW = 1
	}
	rightW := m.width - leftW
	if rightW < 1 {
		rightW = 1
	}

	// stylePane has a 1-cell rounded border on every side and 1 cell of
	// horizontal padding. Width()/Height() set the OUTER dimensions, so
	// the inner content box is (W-4) wide and (H-2) tall.
	innerLeft := leftW - 4 // border (2) + horizontal padding (2)
	innerRight := rightW - 4
	left := stylePane.Width(leftW).MaxWidth(leftW).Height(bodyH).
		Render(clipPane(m.renderTree(innerLeft), innerLeft, bodyH-2))
	right := stylePane.Width(rightW).MaxWidth(rightW).Height(bodyH).
		Render(clipPane(m.renderRight(cur, innerRight, bodyH-2), innerRight, bodyH-2))
	body := lipgloss.JoinHorizontal(lipgloss.Top, left, right)
	return lipgloss.JoinVertical(lipgloss.Left, header, subject, body, footer)
}

func (m programModel) renderHeader(c model.Commit) string {
	tag := tagLabel(c.Tag)
	return styleTitle.Render(fmt.Sprintf(
		"gitfilm  %s ⇒ %s   commit %d/%d   %s   %s   %s",
		m.history.Branch, m.history.Against,
		m.idx+1, len(m.history.Commits),
		c.When.Format("2006-01-02 15:04"),
		c.AuthorName,
		tag,
	))
}

// renderSubject draws a one-line bold commit subject with optional body
// summary, so the *what* and *why* of the current commit is visible
// without squinting at the header.
func (m programModel) renderSubject(c model.Commit, width int) string {
	subject := truncate(c.Subject, width)
	line := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("231")).Render(subject)
	if body := firstNonEmptyLine(c.Body); body != "" {
		line += "  " + styleDim.Render(truncate(body, width-len([]rune(subject))-3))
	}
	return line
}

func firstNonEmptyLine(s string) string {
	for _, ln := range strings.Split(s, "\n") {
		ln = strings.TrimSpace(ln)
		if ln != "" {
			return ln
		}
	}
	return ""
}

func (m programModel) renderTree(width int) string {
	root := m.tree.Snapshot()
	var sb strings.Builder
	renderNode(&sb, root, "", true, width)
	return sb.String()
}

func renderNode(sb *strings.Builder, n *replay.TreeNode, prefix string, isRoot bool, width int) {
	if !isRoot {
		marker := ""
		switch {
		case n.Deleted:
			marker = styleGhost.Render("👻 ")
		case n.NewInThis:
			marker = styleNew.Render("✨ ")
		}
		name := n.Name
		if n.IsDir {
			name = name + "/"
		}

		// Build the row from non-overlapping styled segments. We never
		// nest one .Render() inside another's input — the inner segment's
		// SGR-reset would close the outer style and leave the rest of the
		// row "stuck" in the wrong color, which (combined with the right
		// pane's borders) was bleeding through into the left column.
		var line string
		switch {
		case n.Deleted:
			line = prefix + styleGhost.Render("👻 "+name+" (deleted)")
		case n.IsDir:
			line = prefix + name
		case n.Faint:
			line = prefix + marker + styleGhost.Render(name)
		default:
			touches := ""
			if n.Touches > 0 {
				touches = styleDim.Render(fmt.Sprintf("  ×%d", n.Touches))
			}
			// Color the filename by heat tier — that's the *only* heat
			// signal in the row now. The previous 6-cell bar at the right
			// edge was hard to read and added complex nested ANSI.
			line = prefix + marker + heatNameStyle(n.HeatRatio).Render(name) + touches
		}
		sb.WriteString(truncate(line, width))
		sb.WriteByte('\n')
	}
	for i, c := range n.Children {
		var branch string
		if !isRoot {
			if i == len(n.Children)-1 {
				branch = "└ "
			} else {
				branch = "├ "
			}
		}
		renderNode(sb, c, prefix+branch, false, width)
	}
}

func heatColor(t float64) lipgloss.Color {
	// blue (cool) -> yellow -> red (hot)
	switch {
	case t < 0.25:
		return lipgloss.Color("39")
	case t < 0.5:
		return lipgloss.Color("226")
	case t < 0.75:
		return lipgloss.Color("214")
	default:
		return lipgloss.Color("196")
	}
}

// heatNameStyle styles a file name in the tree by the same heat tier as
// its heat bar — same color so the eye reads "row tone == heat" without
// having to find the small bar at the right edge. The hottest tier is
// also bold so it pops.
func heatNameStyle(ratio float64) lipgloss.Style {
	c := heatColor(ratio)
	s := lipgloss.NewStyle().Foreground(c)
	if ratio >= 0.75 {
		s = s.Bold(true)
	}
	return s
}

func (m programModel) renderRight(c model.Commit, width, height int) string {
	var sb strings.Builder
	// Always lead with a commit-summary card so the right pane is
	// self-contained — the user can read it without glancing at the
	// header. Subject is bright, the meta line below is dim.
	sb.WriteString(styleSubject.Render(truncate(c.Subject, width)))
	sb.WriteByte('\n')
	sb.WriteString(styleDim.Render(fmt.Sprintf("%s · %s · %s · %s",
		c.AuthorName,
		c.When.Format("2006-01-02 15:04"),
		tagLabel(c.Tag),
		c.ShortHash)))
	sb.WriteByte('\n')
	if body := firstNonEmptyLine(c.Body); body != "" {
		sb.WriteString(styleDim.Render(truncate(body, width)))
		sb.WriteByte('\n')
	}
	sb.WriteByte('\n')

	if len(c.Files) == 0 {
		sb.WriteString(styleDim.Render("(no file changes in this commit)"))
		return sb.String()
	}

	// Per-card height budget. We give each card up to 6 lines of diff
	// when we have room; otherwise collapse to 1-line summaries.
	const expandedCardLines = 7
	consumed := 5 // commit card height (subject, meta, body?, separator)
	available := height - consumed
	if available < 4 {
		available = 4
	}
	expandable := available / expandedCardLines
	if expandable < 1 {
		expandable = 1
	}
	if expandable > len(c.Files) {
		expandable = len(c.Files)
	}

	sep := styleDim.Render(strings.Repeat("─", width)) + "\n"
	// Constant typing rate: units accumulate at UnitsPerSecond * playSpeed
	// regardless of dwell duration. Previously units = progress *
	// CommitMaxBudget made the rate balloon when dwell was clamped to
	// MaxCommitMS for big commits — content typed too fast to read.
	units := int(m.effectiveElapsed().Seconds() * replay.UnitsPerSecond)
	for i, f := range c.Files {
		fb := replay.FileBudget(f)
		fileBudget := units
		if fileBudget > fb {
			fileBudget = fb
		}
		anim := replay.ApplyFile(f, fileBudget)
		if i > 0 {
			sb.WriteString(sep)
		}
		if i < expandable {
			sb.WriteString(renderFileCard(f, anim, width))
		} else {
			sb.WriteString(renderFileLine(f, anim, width))
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}

func renderFileLine(f model.FileChange, a replay.FileAnim, width int) string {
	mark := "  "
	if a.Done {
		mark = styleAdd.Render("✓ ")
	} else if a.HunkIdx > 0 || a.LineIdx > 0 || a.CharsInLine > 0 {
		mark = styleNew.Render("▸ ")
	}
	stats := styleAdd.Render(fmt.Sprintf("+%d", f.Added)) + styleDel.Render(fmt.Sprintf(" -%d", f.Removed))
	return fmt.Sprintf("%s%s %s  %s",
		mark,
		statusBadge(f.Status),
		styleFilePath.Render(truncate(f.Path, width-12)),
		stats)
}

func renderFileCard(f model.FileChange, a replay.FileAnim, width int) string {
	var sb strings.Builder
	mark := "▸"
	markStyle := styleNew
	if a.Done {
		mark = "✓"
		markStyle = styleAdd
	}
	stats := styleAdd.Render(fmt.Sprintf("+%d", f.Added)) + styleDel.Render(fmt.Sprintf(" -%d", f.Removed))
	pathLabel := truncate(f.Path, width-12)
	// Bold + brighter file path to make the file card boundary obvious.
	header := markStyle.Render(mark) + " " +
		statusBadge(f.Status) + " " +
		styleFilePath.Render(pathLabel) + "  " + stats
	sb.WriteString(header)
	sb.WriteByte('\n')

	if len(f.Hunks) == 0 {
		return sb.String()
	}
	// Show up to 5 lines around the typing cursor in the active hunk.
	hi := a.HunkIdx
	if hi >= len(f.Hunks) {
		hi = len(f.Hunks) - 1
	}
	h := f.Hunks[hi]
	maxLines := 5
	shown := 0
	for li, l := range h.Lines {
		if shown >= maxLines {
			break
		}
		if !a.Done && li > a.LineIdx {
			break
		}
		text := l.Text
		showCaret := false
		if !a.Done && li == a.LineIdx && l.Kind == model.LineAdded {
			text = replay.PartialLine(l.Text, a.CharsInLine)
			showCaret = true
		}
		switch l.Kind {
		case model.LineAdded:
			line := "  + " + truncate(text, width-5)
			if showCaret {
				line += "▌"
			}
			sb.WriteString(styleAdd.Render(line))
		case model.LineRemoved:
			sb.WriteString(styleDel.Render("  - " + truncate(l.Text, width-5)))
		default:
			sb.WriteString(styleDim.Render("    " + truncate(l.Text, width-5)))
		}
		sb.WriteByte('\n')
		shown++
	}
	return sb.String()
}

func (m programModel) renderFooter() string {
	w := m.width
	if w <= 0 {
		w = 80
	}
	barW := w - 4
	progress := m.renderCommitProgress(barW)
	bar := m.renderTimelineBar(barW)
	legend := strings.Join([]string{
		styleFeat.Render("█ feat"),
		styleAgst.Render("█ " + m.history.Against),
		styleAdd.Render("+ add"),
		styleDel.Render("- del"),
		styleNew.Render("✨ new"),
		styleGhost.Render("👻 deleted"),
		"heat: " +
			lipgloss.NewStyle().Foreground(lipgloss.Color("39")).Render("cool") + " " +
			lipgloss.NewStyle().Foreground(lipgloss.Color("226")).Render("warm") + " " +
			lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Render("hot") + " " +
			lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Bold(true).Render("active"),
	}, "  ")
	speedTag := styleNew.Render(fmt.Sprintf("%.2gx", m.playSpeed))
	hint := styleDim.Render("space: play/pause   ←/→: step   shift+←/→: ±10   +/-: speed (") +
		speedTag + styleDim.Render(", 0:reset)   g/G: ends   q: quit")
	if m.loadErr != nil {
		hint = styleDel.Render("load error: "+m.loadErr.Error()) + "  " + hint
	} else if m.loading && m.loadTotal > 0 {
		pct := len(m.history.Commits) * 100 / m.loadTotal
		hint = styleNew.Render(fmt.Sprintf("loading %d/%d (%d%%)",
			len(m.history.Commits), m.loadTotal, pct)) + "  " + hint
	}
	parts := []string{progress, bar, legend, hint}
	out := strings.Join(parts, "\n")
	return clipPane(out, w, len(parts)+1) // +1 for the caret line inside renderTimelineBar
}

// renderCommitProgress draws a one-line bar for the active commit:
// elapsed dwell vs. its total dwell. Sits directly above the timeline
// strip so the eye reads "where in *this* commit" / "where in the
// *whole* film" as two stacked axes.
func (m programModel) renderCommitProgress(width int) string {
	if width < 4 || len(m.history.Commits) == 0 {
		return ""
	}
	frac := m.commitProgress()
	filled := int(float64(width) * frac)
	if filled > width {
		filled = width
	}
	style := styleNew
	if !m.playing {
		style = styleDim
	}
	return style.Render(strings.Repeat("━", filled)) +
		styleDim.Render(strings.Repeat("─", width-filled))
}

// renderTimelineBar draws a time-based strip: each cell covers a slice
// of wall-clock time, density (commits per neighborhood) is encoded as
// character shade, branch tag as color. Long quiet stretches render as
// dim baselines; busy days as solid blocks.
//
// Density is computed by *windowed sum*, not raw per-cell count.
// Per-cell counts in TUI-width strips (~80–200 cells) are usually 0 or
// 1, which made `count/maxCount` collapse to a binary "filled vs empty"
// look — losing the rhythm we wanted to show. A small sliding window
// smooths this so adjacent activity reinforces, and isolated commits
// stay distinguishable from clusters.
func (m programModel) renderTimelineBar(width int) string {
	if width < 10 || len(m.history.Commits) == 0 {
		return ""
	}
	cells := replay.TimelineBins(m.history.Commits, width)
	density, _ := smoothedDensity(cells, timelineWindow(width))
	q1, q2, q3 := positiveQuartiles(density)
	var sb strings.Builder
	for i, c := range cells {
		ch := densityCharByQuartile(density[i], q1, q2, q3)
		switch {
		case c.Count == 0 && density[i] == 0:
			sb.WriteString(styleDim.Render(ch))
		case c.Tag == model.BranchTagFeature:
			sb.WriteString(styleFeat.Render(ch))
		case c.Tag == model.BranchTagAgainst:
			sb.WriteString(styleAgst.Render(ch))
		default:
			// Empty cell adjacent to activity: tint by neighborhood's
			// dominant tag (look two cells either side).
			sb.WriteString(neighborhoodStyle(cells, i).Render(ch))
		}
	}
	// caret position — by commit time, not commit index, so it tracks
	// the same time axis as the cells.
	frac := replay.TimelineFrac(m.history.Commits, m.idx)
	caret := int(frac * float64(width-1))
	if caret < 0 {
		caret = 0
	}
	if caret >= width {
		caret = width - 1
	}
	return sb.String() + "\n" + strings.Repeat(" ", caret) + styleTitle.Render("▲")
}

// timelineWindow returns the sliding-window radius used to smooth
// per-cell counts. ~5% of the strip width, with a small minimum so
// even very narrow terminals still get some smoothing.
func timelineWindow(width int) int {
	w := width / 20
	if w < 2 {
		w = 2
	}
	if w > 8 {
		w = 8
	}
	return w
}

// smoothedDensity returns a per-cell density value derived from a
// sliding-window sum of cell.Count. Returns the smoothed slice plus the
// max value, so the caller can normalize to 0..1.
func smoothedDensity(cells []replay.TimelineCell, radius int) ([]float64, float64) {
	out := make([]float64, len(cells))
	maxD := 0.0
	for i := range cells {
		sum := 0
		for j := -radius; j <= radius; j++ {
			k := i + j
			if k < 0 || k >= len(cells) {
				continue
			}
			sum += cells[k].Count
		}
		out[i] = float64(sum)
		if out[i] > maxD {
			maxD = out[i]
		}
	}
	return out, maxD
}

// neighborhoodStyle picks a color for an empty cell that sits inside a
// run of activity, so the smoothed shading still reads as feat/against
// instead of falling back to the dim "no commit here" baseline.
func neighborhoodStyle(cells []replay.TimelineCell, i int) lipgloss.Style {
	const r = 3
	feat, agst := 0, 0
	for j := -r; j <= r; j++ {
		k := i + j
		if k < 0 || k >= len(cells) {
			continue
		}
		switch cells[k].Tag {
		case model.BranchTagFeature:
			feat += cells[k].Count
		case model.BranchTagAgainst:
			agst += cells[k].Count
		}
	}
	if feat == 0 && agst == 0 {
		return styleDim
	}
	if feat >= agst {
		return styleFeat
	}
	return styleAgst
}

// positiveQuartiles returns the 25 / 50 / 75 percentile thresholds of
// strictly-positive smoothed values. Quartiles (vs. fixed cutoffs)
// guarantee that even a small history fills four shade tiers — the
// busiest stretch always lands at q3+ ("█"), the quietest active
// stretch at q1- ("░"). Mirrors how GitHub's contribution graph picks
// its 5 levels per-user.
//
// Returns zeros when there are no positive values; callers should
// short-circuit to the empty-baseline character in that case.
func positiveQuartiles(density []float64) (q1, q2, q3 float64) {
	pos := make([]float64, 0, len(density))
	for _, v := range density {
		if v > 0 {
			pos = append(pos, v)
		}
	}
	if len(pos) == 0 {
		return 0, 0, 0
	}
	sort.Float64s(pos)
	pick := func(p float64) float64 {
		i := int(p*float64(len(pos)-1) + 0.5)
		if i < 0 {
			i = 0
		}
		if i >= len(pos) {
			i = len(pos) - 1
		}
		return pos[i]
	}
	return pick(0.25), pick(0.5), pick(0.75)
}

// densityCharByQuartile maps a smoothed-density value to a 5-level
// shade based on quartile thresholds. v == 0 always renders as the
// baseline; positive v uses ░/▒/▓/█ buckets so the busiest cells in
// the strip get the heaviest glyph regardless of absolute activity
// level.
func densityCharByQuartile(v, q1, q2, q3 float64) string {
	if v <= 0 {
		return "·"
	}
	switch {
	case v <= q1:
		return "░"
	case v <= q2:
		return "▒"
	case v <= q3:
		return "▓"
	default:
		return "█"
	}
}

func tagLabel(t model.BranchTag) string {
	switch t {
	case model.BranchTagFeature:
		return styleFeat.Render("[feat]")
	case model.BranchTagAgainst:
		return styleAgst.Render("[main]")
	default:
		return styleDim.Render("[?]")
	}
}

func statusBadge(s model.ChangeStatus) string {
	switch s {
	case model.StatusAdded:
		return styleAdd.Render("A")
	case model.StatusDeleted:
		return styleDel.Render("D")
	case model.StatusRenamed:
		return styleNew.Render("R")
	case model.StatusCopied:
		return styleNew.Render("C")
	default:
		return styleDim.Render("M")
	}
}

// clipPane bounds rendered content to the pane's inner box: at most
// `height` lines, each at most `width` cells wide. The horizontal trim
// is ANSI-aware (preserves color escapes), so colored output won't
// overflow into the neighbouring pane.
func clipPane(s string, width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	lines := strings.Split(s, "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	for i, l := range lines {
		if ansi.StringWidth(l) > width {
			lines[i] = ansi.Truncate(l, width, "")
		}
	}
	return strings.Join(lines, "\n")
}

func truncate(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if len([]rune(s)) <= max {
		return s
	}
	r := []rune(s)
	if max < 1 {
		return ""
	}
	return string(r[:max-1]) + "…"
}
