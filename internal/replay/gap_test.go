package replay

import (
	"testing"
	"time"

	"github.com/S-Nakamur-a/gitfilm/internal/model"
)

func mkCommit(t time.Time) model.Commit {
	return model.Commit{When: t}
}

func TestGapBefore_FirstAndOOB(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cs := []model.Commit{mkCommit(now), mkCommit(now.Add(2 * time.Hour))}
	if d := GapBefore(cs, 0); d != 0 {
		t.Fatalf("first commit should have 0 gap, got %v", d)
	}
	if d := GapBefore(cs, 99); d != 0 {
		t.Fatalf("OOB index should return 0, got %v", d)
	}
}

func TestGapBefore_NonMonotonic(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// Author time can go backwards across rebases — must clamp to 0.
	cs := []model.Commit{mkCommit(now), mkCommit(now.Add(-time.Hour))}
	if d := GapBefore(cs, 1); d != 0 {
		t.Fatalf("backwards gap should clamp to 0, got %v", d)
	}
}

func TestClassifyGap(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want GapTier
	}{
		{30 * time.Minute, GapTierNone},
		{23 * time.Hour, GapTierNone},
		{25 * time.Hour, GapTierHint},
		{6 * 24 * time.Hour, GapTierHint},
		{8 * 24 * time.Hour, GapTierBanner},
		{365 * 24 * time.Hour, GapTierBanner},
	}
	for _, c := range cases {
		if got := ClassifyGap(c.d); got != c.want {
			t.Errorf("ClassifyGap(%v) = %v, want %v", c.d, got, c.want)
		}
	}
}

func TestGapLabel(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{1 * time.Hour, ""},
		{25 * time.Hour, "1 day later"},
		{3 * 24 * time.Hour, "3 days later"},
		{8 * 24 * time.Hour, "1 week later"},
		{45 * 24 * time.Hour, "1 month later"},
		{400 * 24 * time.Hour, "1 year later"},
	}
	for _, c := range cases {
		if got := GapLabel(c.d); got != c.want {
			t.Errorf("GapLabel(%v) = %q, want %q", c.d, got, c.want)
		}
	}
}
