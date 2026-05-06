package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/S-Nakamur-a/gitfilm/internal/model"
	"github.com/charmbracelet/lipgloss"
)

// View is the single Bubble Tea render entry point. It composes
// chrome (header, subject, footer) around the two body panes
// (tree, right). All sub-renderers return plain strings; layout
// happens here.
func (m programModel) View() string {
	if m.width == 0 {
		return "loading…"
	}
	if len(m.history.Commits) == 0 {
		return m.renderLoading()
	}
	cur := m.history.Commits[m.idx]

	// Build chrome first so we can subtract its actual rendered
	// height from the terminal size — guessing constants caused
	// the top of the screen to clip when the subject wrapped.
	header := clipPane(m.renderHeader(cur), m.width, 1)
	subject := clipPane(m.renderSubject(cur, m.width-2), m.width, 1)
	footer := m.renderFooter()
	chromeH := lipgloss.Height(header) + lipgloss.Height(subject) + lipgloss.Height(footer)

	bodyH := m.height - chromeH
	if bodyH < 6 {
		bodyH = 6
	}

	leftW, rightW := splitPaneWidths(m.width)
	body := m.renderBody(cur, leftW, rightW, bodyH)

	return lipgloss.JoinVertical(lipgloss.Left, header, subject, body, footer)
}

func (m programModel) renderLoading() string {
	if m.loadErr != nil {
		return styleDel.Render("load error: " + m.loadErr.Error())
	}
	if m.loadTotal > 0 {
		return fmt.Sprintf("loading commits… (0 / %d)", m.loadTotal)
	}
	return "loading commits…"
}

func (m programModel) renderBody(cur model.Commit, leftW, rightW, bodyH int) string {
	// stylePane has a 1-cell rounded border on every side and 1
	// cell of horizontal padding. Width()/Height() set OUTER
	// dimensions, so the inner content box is (W-4) wide and
	// (H-2) tall.
	const innerPad = 4
	innerLeft := leftW - innerPad
	innerRight := rightW - innerPad

	innerLeftH := bodyH - 2
	leftBody := m.renderLeftPane(innerLeft, innerLeftH)
	left := stylePane.Width(leftW).MaxWidth(leftW).Height(bodyH).
		Render(clipPane(leftBody, innerLeft, innerLeftH))
	right := stylePane.Width(rightW).MaxWidth(rightW).Height(bodyH).
		Render(clipPane(m.renderRight(cur, innerRight, bodyH-2), innerRight, bodyH-2))
	return lipgloss.JoinHorizontal(lipgloss.Top, left, right)
}

// splitPaneWidths divides the terminal width into a left (tree)
// and right (commit) pane. Constraints: leftW + rightW == width
// so JoinHorizontal never produces lines wider than the terminal,
// and each side hits its minimum if there's enough room.
func splitPaneWidths(width int) (leftW, rightW int) {
	leftW = width * 2 / 5
	if leftW < minTreePaneW {
		leftW = minTreePaneW
	}
	if width-leftW < minCommitPaneW {
		leftW = width - minCommitPaneW
	}
	if leftW < 1 {
		leftW = 1
	}
	rightW = width - leftW
	if rightW < 1 {
		rightW = 1
	}
	return leftW, rightW
}

// renderHeader draws the top status line — branch / commit
// counter / commit metadata. Author name is tinted by the
// per-author palette (replay.AuthorColor) so the eye picks up
// "who's on screen" at a glance, the way film viewers track
// characters across cuts.
func (m programModel) renderHeader(c model.Commit) string {
	prefix := styleTitle.Render(fmt.Sprintf(
		"gitfilm  %s ⇒ %s   commit %d/%d   %s   ",
		m.history.Branch, m.history.Against,
		m.idx+1, len(m.history.Commits),
		c.When.Format("2006-01-02 15:04"),
	))
	return prefix + authorChip(c.AuthorName) + "   " + tagLabel(c.Tag)
}

// renderSubject draws a one-line bold commit subject with optional
// body summary, so the *what* and *why* of the current commit is
// visible without squinting at the header.
func (m programModel) renderSubject(c model.Commit, width int) string {
	subject := truncate(c.Subject, width)
	line := styleSubject.Render(subject)
	if body := firstNonEmptyLine(c.Body); body != "" {
		line += "  " + styleDim.Render(truncate(body, width-len([]rune(subject))-3))
	}
	return line
}

// renderFooter draws the bottom chrome: per-commit progress bar,
// time-axis timeline strip with caret, running totals (HUD),
// legend, and key-hint line.
func (m programModel) renderFooter() string {
	w := m.width
	if w <= 0 {
		w = 80
	}
	barW := w - 4
	progress := m.renderCommitProgress(barW)
	bar := m.renderTimelineBar(barW)
	hud := m.renderHUD()
	spark := m.renderMiniGraphs(barW)
	legend := footerLegend(m.history.Against)
	hint := m.footerHint()

	parts := []string{progress, bar, hud}
	if spark != "" {
		parts = append(parts, spark)
	}
	parts = append(parts, legend, hint)
	out := strings.Join(parts, "\n")
	// Bar contributes 2 lines (cells + caret); everything else 1.
	// Compute height from the actual content so future structural
	// changes don't need a magic number here.
	return clipPane(out, w, strings.Count(out, "\n")+1)
}

// renderHUD draws the running-counters line: cumulative
// added/removed/files/days so the user sees the "runtime" of the
// film at a glance, like a stopwatch in the corner of a sports
// broadcast. Reads from the live TreeState so totals are always in
// sync with what's been animated.
func (m programModel) renderHUD() string {
	if len(m.history.Commits) == 0 {
		return ""
	}
	cnt := m.tree.Counts()
	cur := m.history.Commits[m.idx]
	first := m.history.Commits[0].When
	span := cur.When.Sub(first)
	addStr := styleAdd.Render(fmt.Sprintf("+%s", humanCount(cnt.Added)))
	delStr := styleDel.Render(fmt.Sprintf("-%s", humanCount(cnt.Removed)))
	files := styleNew.Render(fmt.Sprintf("%d files", cnt.UniqueFiles))
	dates := styleDim.Render(fmt.Sprintf("%s → %s · %s",
		first.Format("2006-01-02"),
		cur.When.Format("2006-01-02"),
		humanSpan(span)))
	return strings.Join([]string{addStr, delStr, files, dates}, styleDim.Render("  ·  "))
}

// humanCount formats integers with K/M suffixes so the HUD stays
// glanceable on huge histories. 999 stays as "999"; 1500 becomes
// "1.5K"; 2_300_000 becomes "2.3M". Keeps one decimal place
// because zero decimals reads as a step function and feels jumpy
// when scrubbing.
func humanCount(n int) string {
	switch {
	case n < 1000:
		return fmt.Sprintf("%d", n)
	case n < 1_000_000:
		return fmt.Sprintf("%.1fK", float64(n)/1000)
	default:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	}
}

// humanSpan formats a wall-clock duration as the largest sensible
// unit (days/months/years). Same intent as GapLabel but reads as a
// summary ("3 months span") rather than a transition ("3 months
// later").
func humanSpan(d time.Duration) string {
	days := int(d.Hours() / 24)
	switch {
	case days < 1:
		return "today"
	case days < 30:
		return fmt.Sprintf("%dd span", days)
	case days < 365:
		return fmt.Sprintf("%dmo span", days/30)
	default:
		return fmt.Sprintf("%.1fy span", float64(days)/365)
	}
}

func footerLegend(againstName string) string {
	return strings.Join([]string{
		styleFeat.Render("█ feat"),
		styleAgst.Render("█ " + againstName),
		styleAdd.Render("+ add"),
		styleDel.Render("- del"),
		styleNew.Render("✨ new"),
		styleGhost.Render("👻 deleted"),
		"heat: " +
			styleHeatCool.Render("cool") + " " +
			styleHeatWarm.Render("warm") + " " +
			styleHeatHot.Render("hot") + " " +
			styleHeatActive.Render("active"),
	}, "  ")
}

func (m programModel) footerHint() string {
	speedTag := styleNew.Render(fmt.Sprintf("%.2gx", m.playSpeed))
	viewTag := "tree"
	if m.viewMode == ViewModeTreemap {
		viewTag = "treemap"
	}
	hint := styleDim.Render("space: play/pause   ←/→: step   shift+←/→: ±10   +/-: speed (") +
		speedTag + styleDim.Render(", 0:reset)   g/G: ends   t: view (") +
		styleNew.Render(viewTag) + styleDim.Render(")   q: quit")
	if m.loadErr != nil {
		return styleDel.Render("load error: "+m.loadErr.Error()) + "  " + hint
	}
	if m.loading && m.loadTotal > 0 {
		pct := len(m.history.Commits) * 100 / m.loadTotal
		return styleNew.Render(fmt.Sprintf("loading %d/%d (%d%%)",
			len(m.history.Commits), m.loadTotal, pct)) + "  " + hint
	}
	return hint
}
