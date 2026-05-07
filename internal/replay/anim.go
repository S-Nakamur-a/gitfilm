// Package replay holds renderer-agnostic playback policy: animation
// budgets, per-file typing cursors, dwell timing, branch-segment
// collapsing, and the per-frame TreeState used to draw the heat-map.
//
// Both the TUI and the HTML output consume this package so that pacing,
// heat decay, and what counts as a "frame" stay consistent across
// backends. New renderers can depend on replay without re-deriving any
// of the playback math.
//
// File layout:
//
//   - anim.go      animation cost: file budget, profiles, ApplyFile cursor
//   - scramble.go  per-line typing/scramble rendering helpers
//   - dwell.go     per-commit dwell time clamps
//   - tree.go      live filesystem heat-map state (TreeState, snapshots)
//   - segments.go  branch-tag segment collapsing for the timeline
//   - timeline.go  time-binned timeline cells
//   - treemap.go   squarified treemap layout
//   - sparkline.go glyph buckets + downsampling for footer charts
//   - author.go    stable per-author color mapping
//   - gap.go       between-commit wall-clock gaps and labels
package replay

import (
	"github.com/S-Nakamur-a/gitfilm/internal/model"
)

// Animation cost constants. One "unit" ≈ one character of typing speed.
// Pacing knobs (UnitsPerSecond etc.) are calibrated against these — re-run
// `git-film --stats` after any change.
const (
	// LineCost is the visual cost of a full removed/context line.
	LineCost = 4
	// HunkGap is the pause between hunks within a file.
	HunkGap = 6
	// MinFileBudget keeps tiny files (1-line edits) on screen long
	// enough to read instead of flashing by.
	MinFileBudget = 8
	// VisibleLinesPerHunkHTML is how many lines of the first hunk the
	// HTML renderer actually displays. Used by FirstHunkProfile so its
	// budgets don't waste time on lines the user will never see.
	// Bumped from 6 to 15 to mirror the TUI's expanded card capacity —
	// trades payload size for a more readable diff view in the HTML
	// player. Each line adds at most ~width bytes per commit, so 9
	// extra lines × N commits is a low-single-digit MB hit on a 7.9k-
	// commit monorepo.
	VisibleLinesPerHunkHTML = 15
)

// VisibilityProfile describes how much of a file's diff the renderer
// will actually show. Animation budgets respect the profile so dwell
// time matches what the viewer can see.
//
// Zero values mean "no limit": FullProfile = VisibilityProfile{}.
type VisibilityProfile struct {
	// MaxHunks caps the number of hunks animated per file. 0 = all.
	MaxHunks int
	// MaxLinesPerHunk caps lines per hunk. 0 = all.
	MaxLinesPerHunk int
}

// FullProfile shows every line of every hunk. Used by the TUI, which
// progressively reveals each hunk as the cursor advances.
var FullProfile = VisibilityProfile{}

// FirstHunkProfile shows only the first hunk's first VisibleLinesPerHunkHTML
// lines. Used by the HTML renderer.
var FirstHunkProfile = VisibilityProfile{
	MaxHunks:        1,
	MaxLinesPerHunk: VisibleLinesPerHunkHTML,
}

// FileAnim is the animation cursor inside a single file.
// Lines before LineIdx are fully visible; LineIdx is the line currently
// being typed (CharsInLine runes shown, or -1 for a fully-rendered line
// when Done=true).
type FileAnim struct {
	HunkIdx     int
	LineIdx     int
	CharsInLine int
	Done        bool
}

// FileBudget returns the total animation cost (units) of one file's
// diff under FullProfile. Used to size per-file pacing.
func FileBudget(f model.FileChange) int {
	return FileBudgetWith(f, FullProfile)
}

// FileBudgetWith returns the animation cost of the visible portion of
// the file under the given profile.
func FileBudgetWith(f model.FileChange, p VisibilityProfile) int {
	hunks := visibleHunks(f.Hunks, p)
	total := 0
	for hi, h := range hunks {
		lines := visibleLines(h.Lines, p)
		for _, l := range lines {
			if l.Kind == model.LineAdded {
				total += runeCount(l.Text)
			} else {
				total += LineCost
			}
		}
		if hi < len(hunks)-1 {
			total += HunkGap
		}
	}
	if total < MinFileBudget {
		total = MinFileBudget
	}
	return total
}

// ApplyFile is FullProfile shorthand for ApplyFileWith.
func ApplyFile(f model.FileChange, budget int) FileAnim {
	return ApplyFileWith(f, budget, FullProfile)
}

// ApplyFileWith walks one file consuming `budget` units under the given
// profile and returns the resulting cursor. When budget exceeds the
// (visible) total cost, returns Done=true with CharsInLine=-1 (sentinel
// "render full lines").
func ApplyFileWith(f model.FileChange, budget int, p VisibilityProfile) FileAnim {
	if budget <= 0 {
		return FileAnim{}
	}
	hunks := visibleHunks(f.Hunks, p)
	if len(hunks) == 0 {
		return FileAnim{Done: true, CharsInLine: -1}
	}
	rem := budget
	for hi, h := range hunks {
		lines := visibleLines(h.Lines, p)
		for li, l := range lines {
			switch l.Kind {
			case model.LineAdded:
				n := runeCount(l.Text)
				if rem < n {
					return FileAnim{HunkIdx: hi, LineIdx: li, CharsInLine: rem}
				}
				rem -= n
			default:
				if rem < LineCost {
					return FileAnim{HunkIdx: hi, LineIdx: li}
				}
				rem -= LineCost
			}
		}
		if hi < len(hunks)-1 {
			if rem < HunkGap {
				return FileAnim{HunkIdx: hi + 1}
			}
			rem -= HunkGap
		}
	}
	hi, li := lastVisiblePos(hunks, p)
	return FileAnim{Done: true, CharsInLine: -1, HunkIdx: hi, LineIdx: li}
}

// CommitMaxBudget returns the largest FileBudget in a commit under
// FullProfile.
func CommitMaxBudget(c model.Commit) int {
	return CommitMaxBudgetWith(c, FullProfile)
}

// CommitMaxBudgetWith returns the largest FileBudgetWith across files
// in the commit. Used to size dwell so a commit ends when the slowest
// file finishes typing.
func CommitMaxBudgetWith(c model.Commit, p VisibilityProfile) int {
	maxB := 0
	for _, f := range c.Files {
		if b := FileBudgetWith(f, p); b > maxB {
			maxB = b
		}
	}
	if maxB == 0 {
		maxB = MinFileBudget
	}
	return maxB
}

func visibleHunks(hs []model.Hunk, p VisibilityProfile) []model.Hunk {
	if p.MaxHunks > 0 && len(hs) > p.MaxHunks {
		return hs[:p.MaxHunks]
	}
	return hs
}

func visibleLines(ls []model.DiffLine, p VisibilityProfile) []model.DiffLine {
	if p.MaxLinesPerHunk > 0 && len(ls) > p.MaxLinesPerHunk {
		return ls[:p.MaxLinesPerHunk]
	}
	return ls
}

func lastVisiblePos(hunks []model.Hunk, p VisibilityProfile) (hi, li int) {
	if len(hunks) == 0 {
		return 0, 0
	}
	hi = len(hunks) - 1
	lines := visibleLines(hunks[hi].Lines, p)
	if n := len(lines); n > 0 {
		li = n - 1
	}
	return
}

func runeCount(s string) int {
	n := 0
	for range s {
		n++
	}
	return n
}
