package replay

import (
	"sort"
)

// TreemapItem is one input to the treemap layout: a stable key
// (typically a file path) and a positive weight (typically LOC). The
// renderer turns the resulting rectangles back into a colored cell
// using the key — TreemapItem itself does not carry rendering data.
type TreemapItem struct {
	Key    string
	Weight float64
}

// TreemapRect is one output rectangle from a layout pass. (X, Y) is
// the top-left corner; (W, H) the size; coordinates are in the
// caller-supplied unit system (caller passes a width/height when
// invoking and gets coordinates back in the same units).
type TreemapRect struct {
	Key    string
	X, Y   float64
	W, H   float64
	Weight float64
}

// TreemapLayout runs the squarified treemap algorithm
// (Bruls/Huijsen/van Wijk 2000) over `items` inside a w×h rectangle.
// Items are placed in descending weight order so the largest cells
// land in the most visually prominent positions, and the algorithm
// minimizes max aspect ratio per row so cells stay roughly square
// instead of degenerating into long stripes (which is what naive
// slice-and-dice produces).
//
// Items with non-positive weight are skipped. If all weights sum to
// zero, returns nil (the caller should render an empty pane). The
// w/h must be positive — non-positive returns nil too. Output keys
// preserve input order among ties, but otherwise the order is
// determined by the algorithm.
//
// Pure function: same input yields the same output, no allocation
// of caller-visible state. Callers can call this every frame.
func TreemapLayout(items []TreemapItem, w, h float64) []TreemapRect {
	if w <= 0 || h <= 0 || len(items) == 0 {
		return nil
	}
	// Defensive copy + filter + sort. We sort descending; squarified
	// treemap needs the largest first to minimize aspect ratios.
	xs := make([]TreemapItem, 0, len(items))
	totalW := 0.0
	for _, it := range items {
		if it.Weight <= 0 {
			continue
		}
		xs = append(xs, it)
		totalW += it.Weight
	}
	if totalW <= 0 {
		return nil
	}
	sort.SliceStable(xs, func(i, j int) bool { return xs[i].Weight > xs[j].Weight })

	// Normalize weights to area = w*h so we can lay out in pixel
	// space directly. Skipping this would make the math equivalent
	// but require ratio scaling at every step — easier to scale
	// once.
	scale := (w * h) / totalW
	for i := range xs {
		xs[i].Weight *= scale
	}

	out := make([]TreemapRect, 0, len(xs))
	out = squarify(xs, nil, w, h, 0, 0, out)
	return out
}

// squarify is the recursive core. `rect` is the (origin, w, h) of
// the remaining unfilled area. `row` is the in-progress row of
// rectangles; we keep adding items to it while doing so improves
// (or keeps) the worst aspect ratio, then commit the row when adding
// the next item would make it worse.
//
// Recursion bottom-out: when items is empty we flush the in-progress
// row (if any) and return. Tail-recursive in spirit; Go has no TCO
// but the depth is O(rows) which is bounded by sqrt(N) in practice.
func squarify(items, row []TreemapItem, w, h, x, y float64, out []TreemapRect) []TreemapRect {
	if len(items) == 0 {
		return commitRow(row, w, h, x, y, out)
	}
	short := w
	if h < short {
		short = h
	}
	if short <= 0 {
		return commitRow(row, w, h, x, y, out)
	}
	next := append(append([]TreemapItem(nil), row...), items[0])
	if len(row) == 0 || worst(next, short) <= worst(row, short) {
		return squarify(items[1:], next, w, h, x, y, out)
	}
	// Adding the next item makes the row worse: commit current row,
	// then recurse into the leftover rectangle with the next item as
	// the seed of a new row.
	out = commitRow(row, w, h, x, y, out)
	rowSum := sumWeights(row)
	if w <= h {
		// Row was placed along the top edge: shrink the rectangle from
		// the top by rowSum/w.
		dy := rowSum / w
		return squarify(items, nil, w, h-dy, x, y+dy, out)
	}
	dx := rowSum / h
	return squarify(items, nil, w-dx, h, x+dx, y, out)
}

// commitRow lays out the in-progress row along the shorter side of
// the remaining rectangle and appends the resulting TreemapRects to
// `out`. Returns the updated `out` (Go slices are pass-by-value).
func commitRow(row []TreemapItem, w, h, x, y float64, out []TreemapRect) []TreemapRect {
	if len(row) == 0 {
		return out
	}
	rowSum := sumWeights(row)
	if w <= h {
		// Place along width; row height = rowSum / w.
		dy := rowSum / w
		curX := x
		for _, it := range row {
			rw := it.Weight / dy
			out = append(out, TreemapRect{
				Key: it.Key, X: curX, Y: y, W: rw, H: dy, Weight: it.Weight,
			})
			curX += rw
		}
		return out
	}
	// Place along height; row width = rowSum / h.
	dx := rowSum / h
	curY := y
	for _, it := range row {
		rh := it.Weight / dx
		out = append(out, TreemapRect{
			Key: it.Key, X: x, Y: curY, W: dx, H: rh, Weight: it.Weight,
		})
		curY += rh
	}
	return out
}

// worst returns the worst (= largest) aspect ratio that would result
// if the given row were committed against a rectangle whose shorter
// side has length `short`. Aspect ratio is max(w/h, h/w). The
// squarified algorithm advances rows while this stays small.
func worst(row []TreemapItem, short float64) float64 {
	if len(row) == 0 {
		return 0
	}
	sum := sumWeights(row)
	rmin, rmax := row[0].Weight, row[0].Weight
	for _, it := range row[1:] {
		if it.Weight < rmin {
			rmin = it.Weight
		}
		if it.Weight > rmax {
			rmax = it.Weight
		}
	}
	s2 := short * short
	sum2 := sum * sum
	a := s2 * rmax / sum2
	b := sum2 / (s2 * rmin)
	if a > b {
		return a
	}
	return b
}

func sumWeights(row []TreemapItem) float64 {
	s := 0.0
	for _, it := range row {
		s += it.Weight
	}
	return s
}
