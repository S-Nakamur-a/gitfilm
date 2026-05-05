package tui

import (
	"unicode"

	"github.com/S-Nakamur-a/gitplay/internal/model"
)

// Animation cost constants. One "unit" ≈ one character of typing speed.
// All values are calibrated together — bumping unitsPerSecond in
// program.go changes wall-clock pacing without touching these.
const (
	lineCost = 4 // visual cost of a full removed/context line
	hunkGap  = 6 // pause between hunks within a file
	// minFileBudget keeps tiny files (1-line edits) on screen long
	// enough to read instead of flashing by.
	minFileBudget = 8
)

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
// diff. Used to size per-file pacing so a 200-line refactor takes ~10x
// longer than a 20-line tweak.
func FileBudget(f model.FileChange) int {
	total := 0
	for hi, h := range f.Hunks {
		for _, l := range h.Lines {
			if l.Kind == model.LineAdded {
				total += runeCount(l.Text)
			} else {
				total += lineCost
			}
		}
		if hi < len(f.Hunks)-1 {
			total += hunkGap
		}
	}
	if total < minFileBudget {
		total = minFileBudget
	}
	return total
}

// ApplyFile walks one file consuming `budget` units and returns the
// resulting cursor. When budget exceeds the file's total cost, returns
// Done=true with CharsInLine=-1 (sentinel: "render full lines").
func ApplyFile(f model.FileChange, budget int) FileAnim {
	if budget <= 0 {
		return FileAnim{}
	}
	rem := budget
	for hi, h := range f.Hunks {
		for li, l := range h.Lines {
			switch l.Kind {
			case model.LineAdded:
				n := runeCount(l.Text)
				if rem < n {
					return FileAnim{HunkIdx: hi, LineIdx: li, CharsInLine: rem}
				}
				rem -= n
			default:
				if rem < lineCost {
					return FileAnim{HunkIdx: hi, LineIdx: li}
				}
				rem -= lineCost
			}
		}
		if hi < len(f.Hunks)-1 {
			if rem < hunkGap {
				return FileAnim{HunkIdx: hi + 1}
			}
			rem -= hunkGap
		}
	}
	hi, li := lastPos(f)
	return FileAnim{Done: true, CharsInLine: -1, HunkIdx: hi, LineIdx: li}
}

func lastPos(f model.FileChange) (hi, li int) {
	if len(f.Hunks) == 0 {
		return 0, 0
	}
	hi = len(f.Hunks) - 1
	if n := len(f.Hunks[hi].Lines); n > 0 {
		li = n - 1
	}
	return
}

// CommitMaxBudget returns the largest FileBudget in a commit.
// Used to set the commit's dwell time so the whole commit ends when
// the slowest file finishes typing.
func CommitMaxBudget(c model.Commit) int {
	max := 0
	for _, f := range c.Files {
		if b := FileBudget(f); b > max {
			max = b
		}
	}
	if max == 0 {
		max = minFileBudget
	}
	return max
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
