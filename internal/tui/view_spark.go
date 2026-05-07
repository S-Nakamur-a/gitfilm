package tui

import (
	"math"
	"strings"

	"github.com/S-Nakamur-a/gitfilm/internal/replay"
	"github.com/charmbracelet/lipgloss"
)

// Sparkline-specific style tiers. We keep these separate from the
// module-wide styleAdd/styleDel/styleFeat because:
//
//  1. The "past" tier must NOT be bold — otherwise the bold-gold
//     caret blends in. Dropping bold here lets the caret own the only
//     bold cells in the row.
//  2. The "future" tier picks an explicit dimmer hue from the same
//     family instead of relying on lipgloss `.Faint(true)`. SGR 2
//     (faint) is collapsed onto SGR 1 (bold) by most terminals
//     (iTerm2, macOS Terminal, kitty, alacritty), so Faint produced
//     no visible difference when stacked on a bold base. Hard-coding a
//     dim color is terminal-portable and reads as "same series,
//     earlier/later" rather than "different category".
//
// Color picks (xterm 256-color):
//   - 46  / 22  : bright green / dark green   for added lines
//   - 203 / 88  : bright red   / dark red     for removed lines
var (
	sparkAddPast   = lipgloss.NewStyle().Foreground(lipgloss.Color("46"))
	sparkAddFuture = lipgloss.NewStyle().Foreground(lipgloss.Color("22"))
	sparkRemPast   = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	sparkRemFuture = lipgloss.NewStyle().Foreground(lipgloss.Color("88"))
	sparkCaret     = lipgloss.NewStyle().Foreground(lipgloss.Color("220")).Bold(true)
	sparkLabelAdd  = lipgloss.NewStyle().Foreground(lipgloss.Color("46"))
	sparkLabelRem  = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
)

// renderMiniGraphs draws two compact sparklines summarizing per-commit
// churn for the loaded portion of the film:
//
//   - row 1 ("add  "): per-commit added lines (green bars).
//   - row 2 ("rem  "): per-commit removed lines (red bars).
//
// Both rows share a log1p Y axis: bar height is
// log1p(value)/log1p(ceiling). The log compression replaces the
// previous 95th-percentile clip — a 50K-line generated commit and a
// 100-line handwritten commit now both render with visible variation,
// whereas linear scaling crushed everything below the clip into a
// single cell. Adds and removes share one ceiling so a 1000-add bar
// reads as the same height as a 1000-remove bar.
//
// Returns "" when the strip is too narrow or there's nothing to graph
// yet; callers should treat empty as "skip the row".
func (m programModel) renderMiniGraphs(width int) string {
	if width < 16 || len(m.addsAt) == 0 {
		return ""
	}
	const labelW = 6
	graphW := width - labelW
	if graphW < 8 {
		return ""
	}

	loaded := len(m.addsAt)
	caret := replay.CaretBucket(m.idx, loaded, graphW)

	addBins := replay.DownsampleMax(m.addsAt, graphW)
	remBins := replay.DownsampleMax(m.removesAt, graphW)
	ceiling := logCeiling(addBins, remBins)

	addBars := buildLogSparkline(addBins, graphW, caret, ceiling, sparkAddPast, sparkAddFuture)
	remBars := buildLogSparkline(remBins, graphW, caret, ceiling, sparkRemPast, sparkRemFuture)

	row1 := sparkLabelAdd.Render("add   ") + addBars
	row2 := sparkLabelRem.Render("rem   ") + remBars
	return row1 + "\n" + row2
}

// logCeiling returns the shared log1p Y-axis ceiling used by both
// adds and removes so the two halves stay on the same scale. Floor at
// log1p(1) so an all-zero series still produces a defined non-zero
// denominator (the renderer maps zero values to a dim "·" anyway).
func logCeiling(addBins, remBins []float64) float64 {
	m := 1.0
	for _, v := range addBins {
		if v > m {
			m = v
		}
	}
	for _, v := range remBins {
		if v > m {
			m = v
		}
	}
	return math.Log1p(m)
}

// buildLogSparkline renders a binned series at fixed width on a log1p
// Y axis. ceiling must already be log1p(maxValue) so we can divide by
// it directly without recomputing per cell.
//
// caret is the bucket index to highlight; pass -1 to skip the
// "you are here" marker. Rendering uses four tiers:
//
//   - bins  < caret              : `past`        — already played.
//   - bin  == caret              : `sparkCaret`  — current position
//     (bold gold; same on every graph so the user has one consistent
//     "you are here" cue).
//   - bins  > caret              : `future`      — loaded ahead but
//     not yet played; same hue family as `past`, dimmer.
//   - bins past the loaded range : dim "·" baseline — still
//     streaming in. Distinct from "future loaded" so the user can
//     see "what we have vs. what's still coming".
//
// Zero-valued bins also render as "·" instead of the lowest glyph so
// commits with no churn in this direction (e.g. an all-add commit
// shown on the rem row) read as gaps in the beat rather than a faint
// continuous line.
func buildLogSparkline(binned []float64, width, caret int, ceiling float64, past, future lipgloss.Style) string {
	if width <= 0 {
		return ""
	}
	var sb strings.Builder
	for i := range width {
		if i >= len(binned) {
			sb.WriteString(styleDim.Render("·"))
			continue
		}
		var glyph rune
		switch {
		case binned[i] <= 0 || ceiling <= 0:
			glyph = '·'
		default:
			glyph = replay.SparklineGlyph(math.Log1p(binned[i]) / ceiling)
		}
		s := past
		switch {
		case i == caret:
			s = sparkCaret
		case caret >= 0 && i > caret:
			s = future
		}
		sb.WriteString(s.Render(string(glyph)))
	}
	return sb.String()
}
