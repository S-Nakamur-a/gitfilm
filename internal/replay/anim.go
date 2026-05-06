// Package replay holds renderer-agnostic playback policy: animation
// budgets, per-file typing cursors, dwell timing, branch-segment
// collapsing, and the per-frame TreeState used to draw the heat-map.
//
// Both the TUI and the HTML output consume this package so that pacing,
// heat decay, and what counts as a "frame" stay consistent across
// backends. New renderers can depend on replay without re-deriving any
// of the playback math.
package replay

import (
	"unicode"

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
	max := 0
	for _, f := range c.Files {
		if b := FileBudgetWith(f, p); b > max {
			max = b
		}
	}
	if max == 0 {
		max = MinFileBudget
	}
	return max
}

// ScrambleConfig controls the optional "movie decoder" effect layered
// on top of the typing animation. With it on, each line being typed
// renders as locked-prefix + a short stretch of noise that reshuffles
// every frame and snaps to the real character once the cursor reaches
// it. Off (zero value) is plain typing.
type ScrambleConfig struct {
	Enabled bool
	// RevealAhead is how many runes of noise are drawn after the
	// locked prefix. Higher = more "decoding" feel but visually
	// busier. Capped to the remaining line length at render time.
	RevealAhead int
	// Charset is the rune pool the noise is drawn from. Empty falls
	// back to DefaultScrambleCharset. Picking a pool with a similar
	// visual density to the real text reads more like "decoding".
	Charset []rune
}

// DefaultScrambleCharset is the default noise pool: a mix of letters,
// digits, and symbols that gives a "decoder" look without wandering
// into glyphs that may not render in every terminal font.
var DefaultScrambleCharset = []rune("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789!@#$%^&*+=<>?/|{}[]~")

// DefaultScrambleAhead is the recommended RevealAhead.
const DefaultScrambleAhead = 6

// ScrambleLine is PartialLine's "movie" sibling. Returns the locked
// prefix (token-boundary aligned, like PartialLine) followed by up to
// cfg.RevealAhead runes of pseudo-random noise picked from cfg.Charset
// using `seed`. Anything past that range is hidden, so the user never
// sees the un-typed real text.
//
// The seed is XORed with the rune position so each character flickers
// independently — calling this with a different seed every frame
// produces the shimmer; with the same seed the result is stable
// (useful for tests).
//
// Calling with cfg.Enabled=false collapses to PartialLine, so callers
// can branch once on the option and not at every line.
func ScrambleLine(text string, chars int, seed int64, cfg ScrambleConfig) string {
	if !cfg.Enabled {
		return PartialLine(text, chars)
	}
	r := []rune(text)
	if chars >= len(r) {
		return text
	}
	if chars < 0 {
		chars = 0
	}
	stop := chars
	// Only extend at a token boundary when we already have a typed
	// rune to look back at. With chars=0 there is no prefix to
	// "round forward" — the whole line is still in the noise zone.
	if chars > 0 {
		for i := chars; i < len(r) && i < chars+2; i++ {
			if !isWord(r[i-1]) {
				break
			}
			if !isWord(r[i]) {
				stop = i
				break
			}
		}
		if stop > len(r) {
			stop = len(r)
		}
	}

	pool := cfg.Charset
	if len(pool) == 0 {
		pool = DefaultScrambleCharset
	}
	ahead := cfg.RevealAhead
	if ahead < 0 {
		ahead = 0
	}
	end := stop + ahead
	if end > len(r) {
		end = len(r)
	}

	out := make([]rune, 0, end)
	out = append(out, r[:stop]...)
	for i := stop; i < end; i++ {
		out = append(out, pool[scrambleIndex(seed, i, len(pool))])
	}
	return string(out)
}

// scrambleIndex hashes (seed, position) into the charset. splitmix64-
// style: cheap and well-distributed, mixed with `i` so neighbouring
// positions don't move in lockstep.
func scrambleIndex(seed int64, i, mod int) int {
	s := uint64(seed)*6364136223846793005 + uint64(i)*1442695040888963407 + 1
	s ^= s >> 30
	s *= 0xbf58476d1ce4e5b9
	s ^= s >> 27
	s *= 0x94d049bb133111eb
	s ^= s >> 31
	return int(s>>33) % mod
}

// PartialLine returns the visible prefix of an added line based on
// how many runes have been "typed". We split on token boundaries so
// that letters within an identifier appear together while punctuation
// acts as a visible breakpoint — reads as "human typing" rather than
// "scrolling".
func PartialLine(text string, chars int) string {
	if chars <= 0 {
		return ""
	}
	r := []rune(text)
	if chars >= len(r) {
		return text
	}
	stop := chars
	for i := chars; i < len(r) && i < chars+2; i++ {
		if !isWord(r[i-1]) {
			break
		}
		if !isWord(r[i]) {
			stop = i
			break
		}
	}
	if stop > len(r) {
		stop = len(r)
	}
	return string(r[:stop])
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

func isWord(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}

func runeCount(s string) int {
	n := 0
	for range s {
		n++
	}
	return n
}
