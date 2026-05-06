package replay

import (
	"fmt"
	"time"

	"github.com/S-Nakamur-a/gitfilm/internal/model"
)

// GapBefore returns how much wall-clock time elapsed between
// commits[i-1] and commits[i]. Returns 0 for the first commit
// (i==0), out-of-range indices, or non-monotonic timestamps —
// renderers can treat 0 as "no gap to show".
//
// Author time can run backwards across rebases, so we explicitly
// guard with !cur.After(prev) instead of subtracting blindly.
func GapBefore(commits []model.Commit, i int) time.Duration {
	if i <= 0 || i >= len(commits) {
		return 0
	}
	prev := commits[i-1].When
	cur := commits[i].When
	if !cur.After(prev) {
		return 0
	}
	return cur.Sub(prev)
}

// GapTier categorizes a gap into a render strength so renderers can
// decide whether to show nothing, a quiet inline hint, or a more
// distinct banner. Thresholds align with how a person would talk
// about the gap aloud — minutes are "still working", days are a
// hint, weeks are a scene break.
type GapTier int

const (
	// GapTierNone: same session / same day. No card.
	GapTierNone GapTier = iota
	// GapTierHint: 1–7 days. Inline dim "5 days later".
	GapTierHint
	// GapTierBanner: > 7 days. Prominent banner above the subject.
	GapTierBanner
)

// ClassifyGap maps a duration to a tier.
func ClassifyGap(d time.Duration) GapTier {
	switch {
	case d < 24*time.Hour:
		return GapTierNone
	case d < 7*24*time.Hour:
		return GapTierHint
	default:
		return GapTierBanner
	}
}

// GapLabel formats a gap duration as a short human phrase
// ("3 days later", "2 months later", "1 year later"). Returns ""
// for tiers that should not be shown (GapTierNone), so callers can
// branch on emptiness without a separate tier check.
//
// Buckets escalate in human-readable units rather than always
// reporting days, so a multi-year hiatus reads as "2 years later"
// rather than "812 days later".
func GapLabel(d time.Duration) string {
	if ClassifyGap(d) == GapTierNone {
		return ""
	}
	days := int(d.Hours() / 24)
	switch {
	case days < 7:
		return fmt.Sprintf("%d day%s later", days, plural(days))
	case days < 30:
		w := days / 7
		return fmt.Sprintf("%d week%s later", w, plural(w))
	case days < 365:
		mo := days / 30
		return fmt.Sprintf("%d month%s later", mo, plural(mo))
	default:
		yr := days / 365
		return fmt.Sprintf("%d year%s later", yr, plural(yr))
	}
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// BannerExtraDwell returns extra wall-clock time to linger on a
// commit whose preceding gap qualifies as a banner-tier scene break.
// Renderers add this on top of the normal typing dwell so a
// "14 days later" card actually registers before the diff bursts
// in. Capped at a fixed value so a multi-year hiatus doesn't grind
// playback to a halt; the cinematic intent is "noticeable pause",
// not "real-time wait".
func BannerExtraDwell(gap time.Duration) time.Duration {
	if ClassifyGap(gap) != GapTierBanner {
		return 0
	}
	return 800 * time.Millisecond
}
