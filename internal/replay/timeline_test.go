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

func TestTimelineMaxByTime_AlignsWithTimelineBins(t *testing.T) {
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	day := func(d int) time.Time { return base.AddDate(0, 0, d) }

	// Same shape as TestTimelineBins_DensityAndTag: 3 commits at day 0,
	// 1 commit at day 9. Expect peak in cell 0 and a peak in cell 9
	// (the same cells where TimelineBins reports nonzero counts), and
	// zeros in the quiet stretch between.
	commits := []model.Commit{
		tsCommit(day(0), model.BranchTagAgainst),
		tsCommit(day(0), model.BranchTagAgainst),
		tsCommit(day(0), model.BranchTagAgainst),
		tsCommit(day(9), model.BranchTagFeature),
	}
	values := []int{10, 5, 7, 100}
	out := TimelineMaxByTime(commits, values, 10)
	if len(out) != 10 {
		t.Fatalf("got %d cells, want 10", len(out))
	}
	if out[0] != 10 {
		t.Errorf("cell 0 = %v, want 10 (max of 10,5,7)", out[0])
	}
	if out[9] != 100 {
		t.Errorf("cell 9 = %v, want 100", out[9])
	}
	for i := 1; i < 9; i++ {
		if out[i] != 0 {
			t.Errorf("cell %d = %v, want 0 (quiet stretch)", i, out[i])
		}
	}
}

func TestTimelineMaxByTime_Degenerate(t *testing.T) {
	t0 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	commits := []model.Commit{
		tsCommit(t0, model.BranchTagFeature),
		tsCommit(t0, model.BranchTagFeature),
	}
	out := TimelineMaxByTime(commits, []int{3, 7}, 5)
	// All commits share an instant — both values land in the last cell.
	if out[4] != 7 {
		t.Errorf("expected max=7 in last cell, got %+v", out)
	}
	for i := range 4 {
		if out[i] != 0 {
			t.Errorf("cell %d = %v, want 0", i, out[i])
		}
	}
}

func TestTimelineMaxByTime_ShorterValues(t *testing.T) {
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	commits := []model.Commit{
		tsCommit(base, model.BranchTagFeature),
		tsCommit(base.AddDate(0, 0, 5), model.BranchTagFeature),
		tsCommit(base.AddDate(0, 0, 10), model.BranchTagFeature),
	}
	// Streaming: values may lag behind commits during load.
	out := TimelineMaxByTime(commits, []int{4, 9}, 4)
	// Function clamps to min(len(commits), len(values)) = 2 — the third
	// commit has no value yet, so it doesn't contribute.
	if out == nil {
		t.Fatal("got nil out")
	}
	total := 0.0
	for _, v := range out {
		total += v
	}
	if total != 13 {
		t.Errorf("sum = %v, want 13 (4+9 only — third commit has no value)", total)
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
