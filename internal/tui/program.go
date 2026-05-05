package tui

import (
	"fmt"
	"strings"
	"time"

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

type programModel struct {
	history model.History
	tree    *replay.TreeState
	// snapshots[i] is a TreeState clone taken AFTER stepping through
	// commits[0..i*snapshotInterval]. Used to skip most of the replay
	// when jumping backwards on large histories.
	snapshots []*replay.TreeState

	idx          int // current commit index (0-based, oldest first)
	playing      bool
	dwellElapsed time.Duration
	commitDwell  time.Duration // total dwell time for the current commit

	width, height int
}

func runProgram(h model.History) error {
	m := newModel(h)
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
		history: h,
		tree:    st,
		idx:     0,
		playing: true,
	}
	if len(h.Commits) > 0 {
		m.commitDwell = replay.DwellFor(h.Commits[0])
	}
	return m
}

func (m programModel) Init() tea.Cmd { return tick() }

func (m programModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
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
		}
	case tickMsg:
		if m.playing {
			m.dwellElapsed += frameTickMS
			if m.dwellElapsed >= m.commitDwell {
				if m.idx < len(m.history.Commits)-1 {
					m.advance(1)
				} else {
					m.playing = false
				}
			}
		}
		return m, tick()
	}
	return m, nil
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
		m.commitDwell = replay.DwellFor(m.history.Commits[target])
	}
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
	if m.width == 0 || len(m.history.Commits) == 0 {
		return "loading…"
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
	leftW := m.width * 2 / 5
	if leftW < 28 {
		leftW = 28
	}
	rightW := m.width - leftW
	if rightW < 30 {
		rightW = 30
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
	maxHeat := maxNodeHeat(root)
	var sb strings.Builder
	renderNode(&sb, root, "", true, width, maxHeat)
	return sb.String()
}

func renderNode(sb *strings.Builder, n *replay.TreeNode, prefix string, isRoot bool, width int, maxHeat float64) {
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

		var line string
		switch {
		case n.Deleted:
			line = styleGhost.Render(prefix + "👻 " + name + " (deleted)")
		case n.IsDir:
			// dirs always render plain; hot/cold info comes from their
			// children
			line = prefix + name
		case n.Faint:
			// cooled-off file: dim name, no heat bar so the line is calm
			line = styleGhost.Render(prefix + marker + name)
		default:
			heat := heatBar(n.Heat, maxHeat, 6)
			touches := ""
			if n.Touches > 0 {
				touches = styleDim.Render(fmt.Sprintf(" ×%d", n.Touches))
			}
			line = fmt.Sprintf("%s%s%s  %s%s", prefix, marker, name, heat, touches)
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
		renderNode(sb, c, prefix+branch, false, width, maxHeat)
	}
}

func maxNodeHeat(n *replay.TreeNode) float64 {
	max := n.Heat
	for _, c := range n.Children {
		if h := maxNodeHeat(c); h > max {
			max = h
		}
	}
	return max
}

// heatBar renders a small colored gauge representing heat / maxHeat.
func heatBar(heat, max float64, width int) string {
	if max <= 0 || heat <= 0 {
		return styleDim.Render(strings.Repeat("░", width))
	}
	filled := int(float64(width) * (heat / max))
	if filled < 0 {
		filled = 0
	}
	if filled > width {
		filled = width
	}
	bar := strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
	color := heatColor(heat / max)
	return lipgloss.NewStyle().Foreground(color).Render(bar)
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

func (m programModel) renderRight(c model.Commit, width, height int) string {
	progress := 1.0
	if m.commitDwell > 0 {
		progress = float64(m.dwellElapsed) / float64(m.commitDwell)
	}
	if progress > 1 {
		progress = 1
	}

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
	maxBudget := replay.CommitMaxBudget(c)
	units := int(progress * float64(maxBudget))
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
	bar := m.renderTimelineBar(m.width - 4)
	legend := strings.Join([]string{
		styleFeat.Render("█ feat"),
		styleAgst.Render("█ " + m.history.Against),
		styleAdd.Render("+ add"),
		styleDel.Render("- del"),
		styleNew.Render("✨ new"),
		styleGhost.Render("👻 deleted"),
		"heat " + lipgloss.NewStyle().Foreground(lipgloss.Color("39")).Render("░") +
			lipgloss.NewStyle().Foreground(lipgloss.Color("226")).Render("▒") +
			lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Render("▓") +
			lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Render("█"),
	}, "  ")
	hint := styleDim.Render("space: play/pause   ←/→: step   shift+←/→: ±10   g/G: ends   q: quit")
	w := m.width
	if w <= 0 {
		w = 80
	}
	return clipPane(bar+"\n"+legend+"\n"+hint, w, 3)
}

func (m programModel) renderTimelineBar(width int) string {
	if width < 10 || len(m.history.Commits) == 0 {
		return ""
	}
	segs := replay.Segments(m.history.Commits)
	total := len(m.history.Commits)
	var sb strings.Builder
	used := 0
	for i, s := range segs {
		w := s.Len() * width / total
		if i == len(segs)-1 {
			w = width - used
		}
		if w < 1 {
			w = 1
		}
		used += w
		ch := "█"
		switch s.Tag {
		case model.BranchTagFeature:
			sb.WriteString(styleFeat.Render(strings.Repeat(ch, w)))
		case model.BranchTagAgainst:
			sb.WriteString(styleAgst.Render(strings.Repeat(ch, w)))
		default:
			sb.WriteString(styleDim.Render(strings.Repeat(ch, w)))
		}
	}
	// caret position
	caret := m.idx * width / total
	if caret >= width {
		caret = width - 1
	}
	return sb.String() + "\n" + strings.Repeat(" ", caret) + styleTitle.Render("▲")
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
