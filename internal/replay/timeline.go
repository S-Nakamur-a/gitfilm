package replay

import (
	"time"

	"github.com/S-Nakamur-a/gitfilm/internal/model"
)

// TimelineCell is one column of the time-based timeline strip. Cell
// width on screen corresponds to a fixed slice of wall-clock time, so
// quiet periods produce empty cells and bursty days produce dense ones
// — the visual rhythm of the strip mirrors the project's history.
type TimelineCell struct {
	Count   int             // commits whose When falls in this cell
	Density float64         // Count / max(Count); 0..1
	Tag     model.BranchTag // dominant tag in the cell (Unknown if Count == 0)
}

// TimelineBins maps `commits` (ordered oldest -> newest by index, but
// not necessarily by time) onto a strip of `width` cells whose
// horizontal axis is wall-clock time. The result is suitable for both
// the TUI ASCII strip and the HTML SVG/divs.
//
// When all commits share a single instant, every commit lands in the
// last cell — callers should treat that as a degenerate case if they
// want to fall back to even-spacing instead.
func TimelineBins(commits []model.Commit, width int) []TimelineCell {
	if width <= 0 || len(commits) == 0 {
		return nil
	}
	tmin, tmax := timelineSpan(commits)
	span := tmax.Sub(tmin)
	cells := make([]TimelineCell, width)
	type tagCount struct{ feat, agst, oth int }
	perCell := make([]tagCount, width)

	binFor := func(t time.Time) int {
		if span <= 0 {
			return width - 1
		}
		frac := float64(t.Sub(tmin)) / float64(span)
		i := int(frac * float64(width))
		if i >= width {
			i = width - 1
		}
		if i < 0 {
			i = 0
		}
		return i
	}
	for _, c := range commits {
		i := binFor(c.When)
		cells[i].Count++
		switch c.Tag {
		case model.BranchTagFeature:
			perCell[i].feat++
		case model.BranchTagAgainst:
			perCell[i].agst++
		default:
			perCell[i].oth++
		}
	}
	maxCount := 0
	for _, c := range cells {
		if c.Count > maxCount {
			maxCount = c.Count
		}
	}
	for i := range cells {
		if maxCount > 0 {
			cells[i].Density = float64(cells[i].Count) / float64(maxCount)
		}
		pc := perCell[i]
		switch {
		case cells[i].Count == 0:
			cells[i].Tag = model.BranchTagUnknown
		case pc.feat >= pc.agst && pc.feat >= pc.oth:
			cells[i].Tag = model.BranchTagFeature
		case pc.agst >= pc.oth:
			cells[i].Tag = model.BranchTagAgainst
		default:
			cells[i].Tag = model.BranchTagUnknown
		}
	}
	return cells
}

// TimelineMaxByTime bins per-commit `values` onto a time-based strip
// of `width` cells whose horizontal axis matches TimelineBins exactly
// (same tmin/tmax/span). Each cell reports the max of values for
// commits whose When falls in that cell; cells with no commit get 0.
//
// Use this (instead of DownsampleMax) when a sparkline must align
// horizontally with a TimelineBins-rendered strip — DownsampleMax
// bins by commit index, so a long quiet stretch and a busy day each
// occupy the same number of cells, and peaks/caret drift out of sync
// with the time-axis strip above.
func TimelineMaxByTime(commits []model.Commit, values []int, width int) []float64 {
	if width <= 0 || len(commits) == 0 {
		return nil
	}
	n := min(len(commits), len(values))
	out := make([]float64, width)
	if n == 0 {
		return out
	}
	tmin, tmax := timelineSpan(commits[:n])
	span := tmax.Sub(tmin)
	for i := range n {
		var b int
		if span <= 0 {
			b = width - 1
		} else {
			frac := float64(commits[i].When.Sub(tmin)) / float64(span)
			b = int(frac * float64(width))
			if b >= width {
				b = width - 1
			}
			if b < 0 {
				b = 0
			}
		}
		if v := float64(values[i]); v > out[b] {
			out[b] = v
		}
	}
	return out
}

// TimelineFrac returns the 0..1 horizontal position of commits[idx] on
// the time-based timeline. When all commits share one instant, falls
// back to even spacing (idx / (N-1)) so the caret still slides during
// playback.
func TimelineFrac(commits []model.Commit, idx int) float64 {
	if idx < 0 || idx >= len(commits) {
		return 0
	}
	if len(commits) == 1 {
		return 0
	}
	tmin, tmax := timelineSpan(commits)
	span := tmax.Sub(tmin)
	if span <= 0 {
		return float64(idx) / float64(len(commits)-1)
	}
	f := float64(commits[idx].When.Sub(tmin)) / float64(span)
	if f < 0 {
		f = 0
	}
	if f > 1 {
		f = 1
	}
	return f
}

func timelineSpan(commits []model.Commit) (time.Time, time.Time) {
	tmin, tmax := commits[0].When, commits[0].When
	for _, c := range commits {
		if c.When.Before(tmin) {
			tmin = c.When
		}
		if c.When.After(tmax) {
			tmax = c.When
		}
	}
	return tmin, tmax
}
