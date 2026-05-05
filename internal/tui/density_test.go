package tui

import (
	"strings"
	"testing"

	"github.com/S-Nakamur-a/gitfilm/internal/model"
	"github.com/S-Nakamur-a/gitfilm/internal/replay"
)

// TestSmoothedDensity_GivesContrastForSparseRepos guards the case the
// user reported: with one commit per cell, raw density was uniform 1.0.
// Windowed smoothing must produce at least three distinct ratio tiers
// across the strip so the rhythm of bursts vs. lulls reads.
func TestSmoothedDensity_GivesContrastForSparseRepos(t *testing.T) {
	width := 50
	cells := make([]replay.TimelineCell, width)
	// Three clusters of 1-commit cells separated by gaps. Mimics a
	// small repo's wall-clock histogram on a wide-ish terminal.
	for _, i := range []int{2, 3, 4, 25, 26, 45} {
		cells[i].Count = 1
		cells[i].Tag = model.BranchTagFeature
	}
	density, maxD := smoothedDensity(cells, 4)
	if maxD <= 0 {
		t.Fatalf("maxD = %v, want > 0", maxD)
	}
	tiers := map[string]int{}
	for i := range cells {
		var ratio float64
		if maxD > 0 {
			ratio = density[i] / maxD
		}
		tiers[densityChar(cells[i].Count, ratio)]++
	}
	if tiers["·"] == 0 {
		t.Errorf("expected some baseline · cells, got tiers=%v", tiers)
	}
	if tiers["█"] == 0 && tiers["▓"] == 0 {
		t.Errorf("expected at least one strong-density cell, got tiers=%v", tiers)
	}
	distinct := 0
	for _, n := range tiers {
		if n > 0 {
			distinct++
		}
	}
	if distinct < 3 {
		t.Errorf("expected >= 3 distinct visual tiers across strip, got %d (%v)", distinct, tiers)
	}
}

// TestDensityChar_FloorForOccupiedCells locks in the rule that a cell
// with at least one commit always renders darker than the baseline ·,
// even when the smoothed neighborhood is otherwise quiet.
func TestDensityChar_FloorForOccupiedCells(t *testing.T) {
	if got := densityChar(0, 0); got != "·" {
		t.Errorf("empty cell = %q, want ·", got)
	}
	if got := densityChar(1, 0); got == "·" {
		t.Errorf("occupied cell rendered as baseline · — should always be visible")
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
