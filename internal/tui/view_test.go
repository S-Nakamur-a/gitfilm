package tui

import (
	"testing"
	"time"
)

// TestHumanAgo pins the bucket boundaries the timeline axis depends on:
// "today" for sub-day, "Nd ago" up through 29 days, "Nmo ago" up through
// 364 days, "N.Ny ago" beyond a year. The strip is narrow, so a label
// drifting one bucket wider (e.g. "30 days ago" vs "1mo ago") visibly
// changes whether it fits next to the caret.
func TestHumanAgo(t *testing.T) {
	day := 24 * time.Hour
	cases := []struct {
		d    time.Duration
		want string
	}{
		{0, "today"},
		{12 * time.Hour, "today"},
		{day, "1d ago"},
		{5 * day, "5d ago"},
		{29 * day, "29d ago"},
		{30 * day, "1mo ago"},
		{120 * day, "4mo ago"},
		{364 * day, "12mo ago"},
		{365 * day, "1.0y ago"},
		{547 * day, "1.5y ago"},
		{-time.Hour, "today"}, // future commit (post-rebase clock skew)
	}
	for _, c := range cases {
		if got := humanAgo(c.d); got != c.want {
			t.Errorf("humanAgo(%v) = %q, want %q", c.d, got, c.want)
		}
	}
}
