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
