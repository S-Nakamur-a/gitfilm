package tui

import (
	"fmt"
	"strings"

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

	left := stylePane.Width(leftW).MaxWidth(leftW).Height(bodyH).
		Render(clipPane(m.renderTree(innerLeft), innerLeft, bodyH-2))
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
// counter / commit metadata.
func (m programModel) renderHeader(c model.Commit) string {
	return styleTitle.Render(fmt.Sprintf(
		"gitfilm  %s ⇒ %s   commit %d/%d   %s   %s   %s",
		m.history.Branch, m.history.Against,
		m.idx+1, len(m.history.Commits),
		c.When.Format("2006-01-02 15:04"),
		c.AuthorName,
		tagLabel(c.Tag),
	))
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
// time-axis timeline strip with caret, legend, and key-hint line.
func (m programModel) renderFooter() string {
	w := m.width
	if w <= 0 {
		w = 80
	}
	barW := w - 4
	progress := m.renderCommitProgress(barW)
	bar := m.renderTimelineBar(barW)
	legend := footerLegend(m.history.Against)
	hint := m.footerHint()

	parts := []string{progress, bar, legend, hint}
	out := strings.Join(parts, "\n")
	// Bar contributes 2 lines (cells + caret); everything else 1.
	// Compute height from the actual content so future structural
	// changes don't need a magic number here.
	return clipPane(out, w, strings.Count(out, "\n")+1)
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
	hint := styleDim.Render("space: play/pause   ←/→: step   shift+←/→: ±10   +/-: speed (") +
		speedTag + styleDim.Render(", 0:reset)   g/G: ends   q: quit")
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
