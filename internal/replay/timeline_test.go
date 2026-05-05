package replay

import (
	"testing"
	"time"

	"github.com/S-Nakamur-a/gitfilm/internal/model"
)

func tsCommit(t time.Time, tag model.BranchTag) model.Commit {
	return model.Commit{When: t, Tag: tag}
}

func TestTimelineBins_DensityAndTag(t *testing.T) {
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	day := func(d int) time.Time { return base.AddDate(0, 0, d) }

	commits := []model.Commit{
		tsCommit(day(0), model.BranchTagAgainst),
		tsCommit(day(0), model.BranchTagAgainst),
		tsCommit(day(0), model.BranchTagAgainst),
		tsCommit(day(9), model.BranchTagFeature),
	}
	cells := TimelineBins(commits, 10)
	if len(cells) != 10 {
		t.Fatalf("got %d cells, want 10", len(cells))
	}
	if cells[0].Count != 3 {
		t.Errorf("cell 0 count = %d, want 3", cells[0].Count)
	}
	if cells[0].Tag != model.BranchTagAgainst {
		t.Errorf("cell 0 tag = %v, want against", cells[0].Tag)
	}
	if cells[0].Density != 1.0 {
		t.Errorf("cell 0 density = %v, want 1.0", cells[0].Density)
	}
	if cells[9].Count != 1 {
		t.Errorf("cell 9 count = %d, want 1", cells[9].Count)
	}
	if cells[9].Tag != model.BranchTagFeature {
		t.Errorf("cell 9 tag = %v, want feature", cells[9].Tag)
	}
	if cells[9].Density <= 0 || cells[9].Density >= 1 {
		t.Errorf("cell 9 density = %v, expect partial", cells[9].Density)
	}
	for i := 1; i < 9; i++ {
		if cells[i].Count != 0 {
			t.Errorf("cell %d count = %d, want 0 (quiet stretch)", i, cells[i].Count)
		}
	}
}

func TestTimelineBins_Degenerate_AllSameInstant(t *testing.T) {
	t0 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	commits := []model.Commit{
		tsCommit(t0, model.BranchTagFeature),
		tsCommit(t0, model.BranchTagFeature),
	}
	cells := TimelineBins(commits, 5)
	if cells[4].Count != 2 {
		t.Errorf("expected all in last cell, got %+v", cells)
	}
}

func TestTimelineFrac(t *testing.T) {
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	commits := []model.Commit{
		tsCommit(base, model.BranchTagAgainst),
		tsCommit(base.AddDate(0, 0, 5), model.BranchTagFeature),
		tsCommit(base.AddDate(0, 0, 10), model.BranchTagFeature),
	}
	if got := TimelineFrac(commits, 0); got != 0 {
		t.Errorf("frac[0] = %v, want 0", got)
	}
	if got := TimelineFrac(commits, 2); got != 1 {
		t.Errorf("frac[2] = %v, want 1", got)
	}
	if got := TimelineFrac(commits, 1); got < 0.4 || got > 0.6 {
		t.Errorf("frac[1] = %v, want ~0.5", got)
	}
}

func TestTimelineFrac_Degenerate(t *testing.T) {
	t0 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	commits := []model.Commit{
		tsCommit(t0, model.BranchTagFeature),
		tsCommit(t0, model.BranchTagFeature),
	}
	// All same instant — fall back to even spacing.
	if got := TimelineFrac(commits, 1); got != 1.0 {
		t.Errorf("frac[1] = %v, want 1.0 fallback", got)
	}
}
