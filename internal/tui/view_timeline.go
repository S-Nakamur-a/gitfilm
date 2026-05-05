package tui

import (
	"sort"
	"strings"

	"github.com/S-Nakamur-a/gitfilm/internal/model"
	"github.com/S-Nakamur-a/gitfilm/internal/replay"
	"github.com/charmbracelet/lipgloss"
)

// renderCommitProgress draws a one-line bar for the active
// commit: elapsed dwell vs. its total dwell. Sits directly above
// the timeline strip so the eye reads "where in *this* commit"
// stacked over "where in the *whole* film".
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

// renderTimelineBar draws a time-based strip whose horizontal
// axis is wall-clock time. Cell density (commits per
// neighborhood) is encoded as character shade, branch tag as
// color. Long quiet stretches render as dim baselines; busy days
// as solid blocks.
//
// Density is computed by *windowed sum*, not raw per-cell count.
// Per-cell counts in TUI-width strips (~80–200 cells) are usually
// 0 or 1, which made `count/maxCount` collapse to a binary
// "filled vs. empty" look. A small sliding window smooths this so
// adjacent activity reinforces, and isolated commits stay
// distinguishable from clusters.
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
		sb.WriteString(timelineCellStyle(c, density[i], cells, i).Render(ch))
	}

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

func timelineCellStyle(c replay.TimelineCell, density float64, cells []replay.TimelineCell, i int) lipgloss.Style {
	switch {
	case c.Count == 0 && density == 0:
		return styleDim
	case c.Tag == model.BranchTagFeature:
		return styleFeat
	case c.Tag == model.BranchTagAgainst:
		return styleAgst
	default:
		// Empty cell adjacent to activity: tint by neighborhood's
		// dominant tag (look two cells either side).
		return neighborhoodStyle(cells, i)
	}
}

// timelineWindow returns the sliding-window radius used to smooth
// per-cell counts. ~5% of the strip width with a small minimum
// and ceiling so even narrow / very wide terminals render well.
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

// smoothedDensity returns a per-cell density value derived from
// a sliding-window sum of cell.Count. Returns the smoothed slice
// plus the max value (so callers can normalize to 0..1).
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

// neighborhoodStyle picks a color for an empty cell that sits
// inside a run of activity, so the smoothed shading still reads
// as feat/against rather than collapsing to the dim "no commit
// here" baseline.
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

// positiveQuartiles returns the 25 / 50 / 75 percentile
// thresholds of strictly-positive smoothed values. Quartiles (vs.
// fixed cutoffs) guarantee that even a small history fills four
// shade tiers — the busiest stretch always lands at q3+ ("█"),
// the quietest active stretch at q1- ("░"). Mirrors how GitHub's
// contribution graph picks 5 levels per-user.
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

// densityCharByQuartile maps a smoothed-density value to a
// 5-level shade based on quartile thresholds. Zero always renders
// as the baseline; positive values use ░/▒/▓/█ buckets so the
// busiest cells in the strip get the heaviest glyph regardless
// of absolute activity level.
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
