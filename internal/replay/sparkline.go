package replay

import "sort"

// Sparkline glyphs in ascending order. Eight levels — the smallest
// "▁" is one-eighth height, "█" is full. Empty slots use " " so
// caret/marker styling can sit on a clear cell. Kept here as a
// single source of truth so the TUI and any future renderer can't
// drift on which characters represent which buckets.
var sparkRunes = [8]rune{'▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}

// PercentileMax returns the value at the given percentile of the
// strictly-positive entries of values. Used by churn sparklines to
// pick a Y-axis ceiling that ignores extreme outliers (a single
// 50K-line generated-file commit would otherwise crush every other
// bar to a 1-pixel sliver). When the chosen percentile equals or
// undershoots the absolute max, callers should fall back to max so
// the chart still spans full height; PercentileMax does not do that
// substitution itself because the caller usually wants to know the
// ceiling separately for its own clipping decisions.
//
// percentile is clamped to [0, 1]. Zeroes and negatives are dropped
// before percentile calculation — empty input or all-zero input
// returns 0.
func PercentileMax(values []int, percentile float64) int {
	if percentile < 0 {
		percentile = 0
	}
	if percentile > 1 {
		percentile = 1
	}
	pos := make([]int, 0, len(values))
	for _, v := range values {
		if v > 0 {
			pos = append(pos, v)
		}
	}
	if len(pos) == 0 {
		return 0
	}
	sort.Ints(pos)
	idx := int(percentile*float64(len(pos)-1) + 0.5)
	idx = min(max(idx, 0), len(pos)-1)
	return pos[idx]
}

// SparklineGlyph picks a single glyph for a normalized value in
// [0, 1]. Values <= 0 fall to "▁" (the lowest visible glyph) so
// continuous series read as a baseline rather than a gap; the
// caller can substitute " " if it wants gaps to stay invisible.
func SparklineGlyph(norm01 float64) rune {
	if norm01 <= 0 {
		return sparkRunes[0]
	}
	if norm01 >= 1 {
		return sparkRunes[len(sparkRunes)-1]
	}
	idx := int(norm01 * float64(len(sparkRunes)))
	if idx >= len(sparkRunes) {
		idx = len(sparkRunes) - 1
	}
	return sparkRunes[idx]
}

// DownsampleMax bins a slice of values into width buckets and
// returns the per-bucket max. Max (not mean) is used because the
// goal is to show "where the spikes are" — averaging churn across
// a bucket erases the exact thing the sparkline is supposed to
// communicate. For monotonic series (cumulative file counts) the
// max equals the right-edge value, which preserves the "growth
// over time" curve.
//
// Returns nil when width <= 0 or values is empty.
func DownsampleMax(values []int, width int) []float64 {
	if width <= 0 || len(values) == 0 {
		return nil
	}
	if width >= len(values) {
		out := make([]float64, len(values))
		for i, v := range values {
			out[i] = float64(v)
		}
		return out
	}
	out := make([]float64, width)
	n := len(values)
	for i := range width {
		// Half-open bucket [lo, hi). Round to nearest for cell
		// boundaries so the last bucket reaches exactly n.
		lo := i * n / width
		hi := (i + 1) * n / width
		if hi <= lo {
			hi = lo + 1
		}
		if hi > n {
			hi = n
		}
		max := values[lo]
		for j := lo + 1; j < hi; j++ {
			if values[j] > max {
				max = values[j]
			}
		}
		out[i] = float64(max)
	}
	return out
}

// CaretBucket returns the bucket index that an absolute commit
// index maps to under the same binning as DownsampleMax. Used by
// renderers to highlight "you are here" on the sparkline.
//
// total is the total count of values that were binned (NOT just
// the loaded subset — the bucketing is over the full series).
// Returns -1 when the inputs make no sense (so callers can skip
// caret rendering instead of clipping to 0).
func CaretBucket(idx, total, width int) int {
	if width <= 0 || total <= 0 || idx < 0 {
		return -1
	}
	if idx >= total {
		idx = total - 1
	}
	if width >= total {
		return idx
	}
	b := idx * width / total
	if b >= width {
		b = width - 1
	}
	return b
}
