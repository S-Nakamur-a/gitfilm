package tui

import (
	"strings"

	"github.com/S-Nakamur-a/gitfilm/internal/replay"
	"github.com/charmbracelet/lipgloss"
)

// renderMiniGraphs draws two compact sparklines summarizing the
// loaded portion of the film:
//
//   - "churn":  per-commit added+removed (where the spikes are)
//   - "files":  cumulative unique-file count (how the project grew)
//
// The current commit is highlighted on each graph so the user can
// see "you are here" relative to the overall shape. Returns an
// empty string when the strip is too narrow or there's nothing to
// graph yet — callers should treat empty as "skip the row".
func (m programModel) renderMiniGraphs(width int) string {
	if width < 28 || len(m.linesAt) == 0 {
		return ""
	}

	// Layout: two labelled groups separated by spacing. Labels are
	// 6 cells each ("churn ", "files "), the gap between groups is
	// 4 cells of breathing room.
	const (
		labelW = 6
		gap    = 4
	)
	bodyW := width - 2*labelW - gap
	if bodyW < 16 {
		return ""
	}
	graphW := bodyW / 2
	if graphW < 8 {
		return ""
	}

	loaded := len(m.linesAt)
	caret := replay.CaretBucket(m.idx, loaded, graphW)

	churn := buildSparkline(m.linesAt, graphW, caret, styleNew)
	files := buildSparkline(m.filesAt, graphW, caret, styleFeat)

	churnLine := styleDim.Render("churn ") + churn
	filesLine := styleDim.Render("files ") + files
	return churnLine + strings.Repeat(" ", gap) + filesLine
}

// buildSparkline renders one bucketed series at fixed width.
// Empty / all-zero series fall back to a dim baseline of the
// lowest glyph so the graph row still occupies its cells (keeps
// the footer layout stable across terminal resizes).
//
// caret is the bucket index to highlight; pass -1 to skip the
// "you are here" marker. The marker swaps the base color for the
// bold accent so the eye locates it without a glyph change.
func buildSparkline(values []int, width, caret int, base lipgloss.Style) string {
	if width <= 0 {
		return ""
	}
	binned := replay.DownsampleMax(values, width)
	max := 0.0
	for _, v := range binned {
		if v > max {
			max = v
		}
	}
	var sb strings.Builder
	caretStyle := styleNew.Bold(true)
	for i := range width {
		if i >= len(binned) {
			// loaded series is shorter than the strip — pad with
			// dim baselines on the right so the layout doesn't
			// jiggle as commits stream in.
			sb.WriteString(styleDim.Render("·"))
			continue
		}
		var glyph rune
		if max <= 0 {
			glyph = '·'
		} else {
			glyph = replay.SparklineGlyph(binned[i] / max)
		}
		s := base
		if i == caret {
			s = caretStyle
		}
		sb.WriteString(s.Render(string(glyph)))
	}
	return sb.String()
}
