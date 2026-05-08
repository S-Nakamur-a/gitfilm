package tui

import (
	"sort"
	"strings"
	"time"

	"github.com/S-Nakamur-a/gitfilm/internal/model"
	"github.com/S-Nakamur-a/gitfilm/internal/replay"
	"github.com/charmbracelet/lipgloss"
)

// renderCommitProgress draws a one-line bar for the active
// commit: elapsed dwell vs. its total dwell. Sits directly above
// the timeline strip so the eye reads "where in *this* commit"
// stacked over "where in the *whole* film".
//
// Padded on the left by footerGutterW so the bar's start column
// matches the timeline cells and the add/rem sparklines below.
func (m programModel) renderCommitProgress(width int) string {
	if width < footerGutterW+4 || len(m.history.Commits) == 0 {
		return ""
	}
	graphW := width - footerGutterW
	frac := m.commitProgress()
	filled := min(int(float64(graphW)*frac), graphW)
	style := styleNew
	if !m.playing {
		style = styleDim
	}
	return strings.Repeat(" ", footerGutterW) +
		style.Render(strings.Repeat("━", filled)) +
		styleDim.Render(strings.Repeat("─", graphW-filled))
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
//
// `colorMode` selects the visual encoding. ColorModeDefault keeps
// the historical 5-glyph quartile look (works on any terminal).
// ColorModeGradient holds the glyph fixed and varies the foreground
// across an 8-step truecolor brightness ramp per branch tag — finer
// granularity at the cost of needing a truecolor-capable terminal.
func (m programModel) renderTimelineBar(width int) string {
	if width < footerGutterW+10 || len(m.history.Commits) == 0 {
		return ""
	}
	graphW := width - footerGutterW
	cells := replay.TimelineBins(m.history.Commits, graphW)
	density, maxD := smoothedDensity(cells, timelineWindow(graphW))

	var sb strings.Builder
	sb.WriteString(strings.Repeat(" ", footerGutterW))
	switch m.colorMode {
	case ColorModeGlyph:
		q1, q2, q3 := positiveQuartiles(density)
		for i, c := range cells {
			ch := densityCharByQuartile(density[i], q1, q2, q3)
			sb.WriteString(timelineCellStyle(c, density[i], cells, i).Render(ch))
		}
	default:
		for i, c := range cells {
			sb.WriteString(gradientCell(c, density[i], maxD, cells, i))
		}
	}

	frac := replay.TimelineFrac(m.history.Commits, m.idx)
	caret := min(max(int(frac*float64(graphW-1)), 0), graphW-1)
	axisRow := strings.Repeat(" ", footerGutterW) + m.renderTimeAxisRow(graphW, caret)
	return sb.String() + "\n" + axisRow
}

// renderTimeAxisRow composes the row beneath the timeline cells. It
// carries the caret triangle (current playback position on the
// time axis) plus a left "N ago" label anchored to the oldest
// commit and a right "N ago" label anchored to the newest commit.
//
// Labels are dropped when they would collide with the caret or with
// each other — the caret is the primary signal and always wins. At
// playback start the right label ("today") provides an anchor for
// where the timeline ends; near the end the left label ("1y ago")
// reminds the viewer where it began. Together they keep the strip
// readable without consuming an extra row.
//
// caret must be in [0, graphW-1]; callers enforce that bound.
func (m programModel) renderTimeAxisRow(graphW, caret int) string {
	if graphW <= 0 || len(m.history.Commits) == 0 {
		return ""
	}
	now := time.Now()
	commits := m.history.Commits
	leftLabel := humanAgo(now.Sub(commits[0].When))
	rightLabel := humanAgo(now.Sub(commits[len(commits)-1].When))

	leftLen, rightLen := len(leftLabel), len(rightLabel)
	const labelGap = 1 // min visible gap between a label and the caret
	// Both labels must fit alongside the caret; if not, hide the closer one.
	canShowLeft := leftLen+labelGap <= caret
	canShowRight := caret+1+labelGap <= graphW-rightLen
	// And labels must not overlap each other.
	if leftLen+labelGap > graphW-rightLen {
		canShowLeft = false
		canShowRight = false
	}

	var sb strings.Builder
	col := 0
	if canShowLeft {
		sb.WriteString(styleDim.Render(leftLabel))
		col = leftLen
	}
	if caret > col {
		sb.WriteString(strings.Repeat(" ", caret-col))
		col = caret
	}
	sb.WriteString(styleTitle.Render("▲"))
	col = caret + 1
	rightStart := graphW - rightLen
	gapEnd := graphW
	if canShowRight {
		gapEnd = rightStart
	}
	if gapEnd > col {
		sb.WriteString(strings.Repeat(" ", gapEnd-col))
	}
	if canShowRight {
		sb.WriteString(styleDim.Render(rightLabel))
	}
	return sb.String()
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

// --- gradient encoding ---------------------------------------------------
//
// In gradient mode we hold the glyph fixed at "█" (so cell area is
// constant) and vary the foreground color. Each branch tag has its
// own ramp from a dim baseline to a fully-saturated peak,
// generated by linear RGB interpolation between two anchor hex
// colors. That keeps the tag→hue contract from glyph mode while
// giving fine intra-tag granularity instead of 4 quartile buckets.
//
// gradientSteps controls the number of distinct shades per tag.
// Capped at 12 because:
//   - Input granularity (smoothedDensity returns small integer
//     sums) saturates around 8-12 distinct values for sparse
//     repos; more steps would alias to the same densities.
//   - Human JND on a single-cell colored ramp is ~12-15 steps;
//     above that the visual difference is invisible.
//   - 256-color terminals can't render >~6 truecolor steps per
//     hue without collisions, so the cliff above 12 is steep.
//
// Truecolor (24-bit) is required for the ramp to read smoothly.
// Lipgloss falls back to the nearest ANSI 256 entry on lesser
// terminals (visible banding) and to dim/normal on 16-color
// terminals; users on those should pick --color-mode=glyph.
const gradientSteps = 12

var (
	featAnchorLow  = mustParseHex("#3d2740")
	featAnchorHigh = mustParseHex("#ffb6e1")
	agstAnchorLow  = mustParseHex("#1f2a3a")
	agstAnchorHigh = mustParseHex("#8accef")

	featGradient    = buildRamp(featAnchorLow, featAnchorHigh, gradientSteps)
	againstGradient = buildRamp(agstAnchorLow, agstAnchorHigh, gradientSteps)

	emptyBaseline = "#3a3a3a"
)

// buildRamp returns n hex colors evenly spaced between lo and hi in
// linear RGB. Linear-RGB interpolation is "good enough" for a small
// ramp on a single-cell strip — we don't go through OkLab/HSL
// because the perceptual error at this size is below JND.
func buildRamp(lo, hi [3]uint8, n int) []string {
	out := make([]string, n)
	if n == 1 {
		out[0] = rgbHex(lo)
		return out
	}
	for i := 0; i < n; i++ {
		t := float64(i) / float64(n-1)
		var c [3]uint8
		for k := 0; k < 3; k++ {
			c[k] = uint8(float64(lo[k]) + t*(float64(hi[k])-float64(lo[k])) + 0.5)
		}
		out[i] = rgbHex(c)
	}
	return out
}

func rgbHex(c [3]uint8) string {
	const hex = "0123456789abcdef"
	b := []byte{'#',
		hex[c[0]>>4], hex[c[0]&0x0f],
		hex[c[1]>>4], hex[c[1]&0x0f],
		hex[c[2]>>4], hex[c[2]&0x0f],
	}
	return string(b)
}

// mustParseHex parses #rrggbb at init-time. Panics on bad input
// because the only callers are package-level constants — a typo
// there is a programming error, not a runtime condition.
func mustParseHex(s string) [3]uint8 {
	if len(s) != 7 || s[0] != '#' {
		panic("invalid hex color: " + s)
	}
	var out [3]uint8
	for i := 0; i < 3; i++ {
		hi := hexNibble(s[1+i*2])
		lo := hexNibble(s[1+i*2+1])
		out[i] = uint8(hi<<4 | lo)
	}
	return out
}

func hexNibble(b byte) int {
	switch {
	case b >= '0' && b <= '9':
		return int(b - '0')
	case b >= 'a' && b <= 'f':
		return int(b-'a') + 10
	case b >= 'A' && b <= 'F':
		return int(b-'A') + 10
	}
	panic("invalid hex nibble")
}

// gradientCell renders a single timeline cell using the brightness-
// ramp encoding. Empty cells inside an active neighborhood pick the
// dominant tag's lowest ramp step so the strip flows continuously
// instead of dropping to baseline mid-cluster.
func gradientCell(c replay.TimelineCell, density, maxD float64, cells []replay.TimelineCell, i int) string {
	if c.Count == 0 && density == 0 {
		return styleDim.Render("·")
	}

	tag := cellTag(c, cells, i)
	ramp := gradientForTag(tag)
	step := gradientStep(density, maxD, len(ramp))

	if c.Count == 0 {
		// Quiet cell adjacent to activity: draw the baseline of the
		// dominant ramp so the eye reads continuity without claiming
		// a commit landed here.
		return lipgloss.NewStyle().Foreground(lipgloss.Color(emptyBaseline)).Render("░")
	}

	return lipgloss.NewStyle().Foreground(lipgloss.Color(ramp[step])).Render("█")
}

// cellTag picks which ramp a cell belongs to. For empty cells we
// borrow neighborhoodStyle's logic and look at the surrounding tags
// so transitions stay smooth across a feat→against handoff.
func cellTag(c replay.TimelineCell, cells []replay.TimelineCell, i int) model.BranchTag {
	if c.Count > 0 {
		return c.Tag
	}
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
	if feat >= agst {
		return model.BranchTagFeature
	}
	return model.BranchTagAgainst
}

func gradientForTag(t model.BranchTag) []string {
	if t == model.BranchTagAgainst {
		return againstGradient
	}
	return featGradient
}

// gradientStep maps a positive density to a ramp index. Uses
// max-normalized linear mapping; with width-many bins and a small
// smoothing window, density spans roughly [1, ~2*window+1] so the
// 8-step ramp gets exercised across its full range.
func gradientStep(v, maxD float64, n int) int {
	if v <= 0 || maxD <= 0 {
		return 0
	}
	step := int(v / maxD * float64(n-1))
	if step < 0 {
		step = 0
	}
	if step >= n {
		step = n - 1
	}
	return step
}
