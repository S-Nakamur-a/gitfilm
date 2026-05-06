package tui

import (
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
// Churn is bipolar: adds use the green family, removes use the red
// family, and both share a 95th-percentile clip so a single huge
// generated-file commit doesn't crush every other bar to one cell.
//
// Color picks (xterm 256-color):
//   - 46  / 22  : bright green / dark green   for added lines
//   - 203 / 88  : bright red   / dark red     for removed lines
//   - 213 / 96  : bright pink  / dim violet   for files (cumulative)
var (
	sparkAddPast    = lipgloss.NewStyle().Foreground(lipgloss.Color("46"))
	sparkAddFuture  = lipgloss.NewStyle().Foreground(lipgloss.Color("22"))
	sparkRemPast    = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	sparkRemFuture  = lipgloss.NewStyle().Foreground(lipgloss.Color("88"))
	sparkFilesPast  = lipgloss.NewStyle().Foreground(lipgloss.Color("213"))
	sparkFilesFut   = lipgloss.NewStyle().Foreground(lipgloss.Color("96"))
	sparkCaret      = lipgloss.NewStyle().Foreground(lipgloss.Color("220")).Bold(true)
	sparkLabelAdd   = lipgloss.NewStyle().Foreground(lipgloss.Color("46"))
	sparkLabelRem   = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	sparkLabelFiles = styleDim
)

// outlierClipPercentile is where we cap the bipolar churn Y axis.
// Picked empirically: real human-authored commits live in the lower
// 95% of the distribution; the top 5% is dominated by generated-file
// dumps, vendored library imports, lockfile rewrites, and similar
// "not really code" events. Letting those define max would crush
// every interesting bar to a single pixel.
const outlierClipPercentile = 0.95

// renderMiniGraphs draws three compact sparklines summarizing the
// loaded portion of the film:
//
//   - row 1 ("add  "): per-commit added lines (green bars)
//   - row 2 ("rem  "): per-commit removed lines (red bars), inline
//     with files cumulative on the right of the same row
//   - or — when terminal is wide enough — files renders inline on
//     row 1 next to "add" and row 2 carries only "rem".
//
// The two-row layout is the price we pay for honest bipolar churn —
// signed bars can't be packed into a single TUI row without losing
// either magnitude or per-direction clarity.
//
// Returns an empty string when the strip is too narrow or there's
// nothing to graph yet; callers should treat empty as "skip the row".
func (m programModel) renderMiniGraphs(width int) string {
	if width < 28 || len(m.addsAt) == 0 {
		return ""
	}

	// Layout: each labelled group is 6 cells of label + sparkline.
	// We aim to fit "add | files" on one row and "rem" on the next,
	// so files can stay aligned with the X axis of the bipolar churn.
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

	loaded := len(m.addsAt)
	caret := replay.CaretBucket(m.idx, loaded, graphW)

	// Symmetric clip: the same ceiling for adds and removes so a
	// 1000-add bar reads visually equal in height to a 1000-remove
	// bar. Without this the two halves auto-scale independently and
	// the user can't compare them.
	clip := bipolarClip(m.addsAt, m.removesAt)

	addBars := buildSparklineClipped(m.addsAt, graphW, caret, sparkAddPast, sparkAddFuture, clip)
	remBars := buildSparklineClipped(m.removesAt, graphW, caret, sparkRemPast, sparkRemFuture, clip)
	files := buildSparkline(m.filesAt, graphW, caret, sparkFilesPast, sparkFilesFut)

	// Row 1: add + files  (files on the same row keeps the footer
	// from growing 3 lines tall just because we went bipolar).
	// Row 2: rem (with the file label cell blanked so the columns
	// still line up).
	row1 := sparkLabelAdd.Render("add   ") + addBars +
		strings.Repeat(" ", gap) +
		sparkLabelFiles.Render("files ") + files
	row2 := sparkLabelRem.Render("rem   ") + remBars

	return row1 + "\n" + row2
}

// bipolarClip returns the symmetric Y-axis ceiling for the bipolar
// churn chart. We take the 95th percentile of the *combined* adds +
// removes positive distribution so a few massive add-only or
// remove-only commits both contribute to picking the threshold —
// otherwise a repo that does 99% adds would clip removes to nothing.
// The ceiling is at least 1 so all-zero / all-clipped bins still
// produce a baseline glyph instead of dividing by zero.
func bipolarClip(adds, removes []int) int {
	combined := make([]int, 0, len(adds)+len(removes))
	combined = append(combined, adds...)
	combined = append(combined, removes...)
	return max(replay.PercentileMax(combined, outlierClipPercentile), 1)
}

// buildSparkline renders one bucketed series at fixed width with no
// clip — the Y axis spans 0 to the in-strip max. Used for files
// (cumulative monotonic series) where outliers don't apply.
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
func buildSparkline(values []int, width, caret int, past, future lipgloss.Style) string {
	return buildSparklineClipped(values, width, caret, past, future, 0)
}

// buildSparklineClipped is the same as buildSparkline but caps the
// Y-axis at clip when clip > 0. Bins at or above clip render as the
// full-height glyph; smaller bins use proportional glyphs against
// the clip ceiling. This makes a "huge generated commit" bar visually
// equivalent to a "moderately huge handwritten commit" bar — both
// peg out at full height — but small commits remain readable
// alongside them.
func buildSparklineClipped(values []int, width, caret int, past, future lipgloss.Style, clip int) string {
	if width <= 0 {
		return ""
	}
	binned := replay.DownsampleMax(values, width)
	ceiling := float64(clip)
	if clip <= 0 {
		ceiling = 0
		for _, v := range binned {
			if v > ceiling {
				ceiling = v
			}
		}
	}
	var sb strings.Builder
	for i := range width {
		if i >= len(binned) {
			sb.WriteString(styleDim.Render("·"))
			continue
		}
		var glyph rune
		switch {
		case ceiling <= 0:
			glyph = '·'
		case binned[i] >= ceiling:
			glyph = '█'
		default:
			glyph = replay.SparklineGlyph(binned[i] / ceiling)
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
