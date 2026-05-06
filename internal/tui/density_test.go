package tui

import (
	"strings"
	"testing"

	"github.com/S-Nakamur-a/gitfilm/internal/model"
	"github.com/S-Nakamur-a/gitfilm/internal/replay"
)

// TestSmoothedDensity_GivesContrastForSparseRepos guards the case the
// user reported: with one commit per cell, raw density was uniform 1.0.
// Windowed smoothing + quartile thresholds must produce all four shade
// tiers (░ ▒ ▓ █) plus the baseline · across the strip so the rhythm
// of bursts vs. lulls reads.
func TestSmoothedDensity_GivesContrastForSparseRepos(t *testing.T) {
	width := 80
	cells := make([]replay.TimelineCell, width)
	// A graded distribution: a single commit, a small cluster, a big
	// cluster, separated by long gaps. This is what the GitHub-grass
	// look needs — mixed densities across the strip.
	for _, i := range []int{2, 25, 26, 27, 60, 61, 62, 63, 64, 65, 66, 67} {
		cells[i].Count = 1
		cells[i].Tag = model.BranchTagFeature
	}
	density, maxD := smoothedDensity(cells, 4)
	if maxD <= 0 {
		t.Fatalf("maxD = %v, want > 0", maxD)
	}
	q1, q2, q3 := positiveQuartiles(density)
	tiers := map[string]int{}
	for i := range cells {
		tiers[densityCharByQuartile(density[i], q1, q2, q3)]++
	}
	for _, want := range []string{"·", "░", "▒", "▓", "█"} {
		if tiers[want] == 0 {
			t.Errorf("expected at least one %q cell, got tiers=%v", want, tiers)
		}
	}
}

// TestDensityCharByQuartile_BaselineForZero locks in the rule that
// only zero-density cells render as the baseline · — any positive
// density must produce a visible glyph.
func TestDensityCharByQuartile_BaselineForZero(t *testing.T) {
	if got := densityCharByQuartile(0, 1, 2, 3); got != "·" {
		t.Errorf("zero density = %q, want ·", got)
	}
	if got := densityCharByQuartile(0.1, 1, 2, 3); got == "·" {
		t.Errorf("positive density rendered as baseline · — should always be visible")
	}
}

// TestParseColorMode covers every accepted spelling of the
// --color-mode flag plus rejection of typos. Empty string maps to
// gradient (the CLI default) so callers that build Options{}
// directly land on the same look as the CLI.
func TestParseColorMode(t *testing.T) {
	cases := []struct {
		in      string
		want    ColorMode
		wantErr bool
	}{
		{"", ColorModeGradient, false},
		{"gradient", ColorModeGradient, false},
		{"glyph", ColorModeGlyph, false},
		{"GRADIENT", ColorModeGradient, true}, // case-sensitive on purpose
		{"default", ColorModeGradient, true},  // legacy name no longer accepted
		{"rainbow", ColorModeGradient, true},
	}
	for _, c := range cases {
		got, err := ParseColorMode(c.in)
		if (err != nil) != c.wantErr {
			t.Errorf("ParseColorMode(%q) err = %v, wantErr=%v", c.in, err, c.wantErr)
		}
		if !c.wantErr && got != c.want {
			t.Errorf("ParseColorMode(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// TestGradientStep covers the density→ramp-index mapping. Zero
// density must always pin to step 0 (so "no commits here" stays
// visually distinct from "barely any commits"), and the busiest
// cell must reach the top of the ramp.
func TestGradientStep(t *testing.T) {
	cases := []struct {
		v, max float64
		n      int
		want   int
	}{
		{0, 1, 8, 0},
		{1, 1, 8, 7},
		{0.5, 1, 8, 3},
		{2, 1, 8, 7},  // clamps to top
		{1, 0, 8, 0},  // guards against div-by-zero
		{-1, 1, 8, 0}, // negative density treated as zero
	}
	for _, c := range cases {
		got := gradientStep(c.v, c.max, c.n)
		if got != c.want {
			t.Errorf("gradientStep(%v, %v, %d) = %d, want %d", c.v, c.max, c.n, got, c.want)
		}
	}
}

// TestGradientForTag asserts the two ramps are distinct: feat and
// against share length but never overlap on any step. This is the
// load-bearing property — losing it collapses the two-tag visual
// channel into a single hue.
func TestGradientForTag(t *testing.T) {
	feat := gradientForTag(model.BranchTagFeature)
	agst := gradientForTag(model.BranchTagAgainst)
	if len(feat) != gradientSteps || len(agst) != gradientSteps {
		t.Fatalf("ramp lengths = (%d, %d), want both = %d", len(feat), len(agst), gradientSteps)
	}
	for i := range feat {
		if feat[i] == agst[i] {
			t.Errorf("ramps overlap at step %d: %s", i, feat[i])
		}
	}
}

// TestGradientRamp_Endpoints locks in that interpolation preserves
// the anchor colors exactly at step 0 and step n-1. If a future
// refactor switches to a non-linear interpolation (HSL/OkLab) the
// anchors might drift — this test catches that.
func TestGradientRamp_Endpoints(t *testing.T) {
	cases := []struct {
		name           string
		ramp           []string
		wantLo, wantHi string
	}{
		{"feat", featGradient, rgbHex(featAnchorLow), rgbHex(featAnchorHigh)},
		{"against", againstGradient, rgbHex(agstAnchorLow), rgbHex(agstAnchorHigh)},
	}
	for _, c := range cases {
		if c.ramp[0] != c.wantLo {
			t.Errorf("%s ramp[0] = %s, want %s", c.name, c.ramp[0], c.wantLo)
		}
		if c.ramp[len(c.ramp)-1] != c.wantHi {
			t.Errorf("%s ramp[last] = %s, want %s", c.name, c.ramp[len(c.ramp)-1], c.wantHi)
		}
	}
}

// TestGradientRamp_MonotonicBrightness verifies each ramp's
// per-channel sum (a rough brightness proxy) increases strictly
// with step index. Non-monotonic ramps would make "more density"
// occasionally read as "less" — semantically broken.
func TestGradientRamp_MonotonicBrightness(t *testing.T) {
	for _, ramp := range [][]string{featGradient, againstGradient} {
		prev := -1
		for i, hex := range ramp {
			c := mustParseHex(hex)
			sum := int(c[0]) + int(c[1]) + int(c[2])
			if sum <= prev {
				t.Errorf("ramp non-monotonic at step %d: %s sum=%d, prev=%d", i, hex, sum, prev)
			}
			prev = sum
		}
	}
}

// TestRenderTimelineBar_HasShadingVariation runs the actual TUI strip
// renderer against a sparse history and asserts the resulting line
// contains more than one block-element character (i.e. real visible
// shading, not a uniform fill).
func TestRenderTimelineBar_HasShadingVariation(t *testing.T) {
	commits := []model.Commit{}
	base := mustParse("2025-01-01T00:00:00Z")
	for _, h := range []int{0, 1, 2, 200, 201, 400} {
		commits = append(commits, model.Commit{
			When: base.Add(hour(h)),
			Tag:  model.BranchTagFeature,
		})
	}
	pm := programModel{
		history: model.History{Commits: commits, Branch: "feat", Against: "main"},
		idx:     0,
	}
	out := pm.renderTimelineBar(80)
	if out == "" {
		t.Fatal("expected non-empty timeline output")
	}
	// First line carries the cells; second line carries the caret.
	stripLine := strings.SplitN(out, "\n", 2)[0]
	chars := map[rune]int{}
	for _, r := range stripLine {
		switch r {
		case '·', '░', '▒', '▓', '█':
			chars[r]++
		}
	}
	distinct := 0
	for _, n := range chars {
		if n > 0 {
			distinct++
		}
	}
	if distinct < 3 {
		t.Errorf("timeline strip lacks shading variation: distinct chars = %d (%v)", distinct, chars)
	}
}
