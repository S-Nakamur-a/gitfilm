package replay

import "github.com/S-Nakamur-a/gitfilm/internal/model"

// Segment is a contiguous run of commits sharing the same BranchTag.
// Used to draw the colored timeline at the bottom of any renderer.
type Segment struct {
	Tag   model.BranchTag
	Start int // inclusive
	End   int // inclusive
}

func (s Segment) Len() int { return s.End - s.Start + 1 }

// Segments collapses a commit slice into runs of equal tag.
func Segments(commits []model.Commit) []Segment {
	if len(commits) == 0 {
		return nil
	}
	var out []Segment
	cur := Segment{Tag: commits[0].Tag, Start: 0, End: 0}
	for i := 1; i < len(commits); i++ {
		if commits[i].Tag == cur.Tag {
			cur.End = i
			continue
		}
		out = append(out, cur)
		cur = Segment{Tag: commits[i].Tag, Start: i, End: i}
	}
	out = append(out, cur)
	return out
}
