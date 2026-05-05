package replay

import (
	"time"

	"github.com/S-Nakamur-a/gitplay/internal/model"
)

// Pacing knobs. Vars (not consts) so tests / future flags can override.
// All values are calibrated together — bumping one in isolation usually
// breaks dwell feel; re-run `git-play --stats` after changes.
var (
	// UnitsPerSecond controls how fast we burn through the per-file
	// animation budget. ~200 unit/s feels brisk without losing the
	// "watching it being typed" effect. Per-file budgets scale with
	// content size so large diffs naturally take longer.
	UnitsPerSecond = 200.0
	// MinCommitMS / MaxCommitMS clamp dwell so trivial commits don't
	// flash by and refactors don't grind everything to a halt.
	MinCommitMS = 250 * time.Millisecond
	MaxCommitMS = 3 * time.Second
)

// DwellFor returns how long the animation should spend on a commit
// under FullProfile (TUI pacing).
func DwellFor(c model.Commit) time.Duration {
	return DwellForWith(c, FullProfile)
}

// DwellForWith returns how long the animation should spend on a commit
// under the given visibility profile. Each file animates in parallel at
// a constant typing cadence, so the commit ends when the slowest (=
// largest visible) file finishes. The clamp keeps trivial commits
// visible and stops huge commits from grinding the timeline to a halt.
func DwellForWith(c model.Commit, p VisibilityProfile) time.Duration {
	secs := float64(CommitMaxBudgetWith(c, p)) / UnitsPerSecond
	d := time.Duration(secs * float64(time.Second))
	if d < MinCommitMS {
		d = MinCommitMS
	}
	if d > MaxCommitMS {
		d = MaxCommitMS
	}
	return d
}
