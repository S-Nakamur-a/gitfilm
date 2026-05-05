package replay

import (
	"reflect"
	"testing"

	"github.com/S-Nakamur-a/gitplay/internal/model"
)

func tagged(tags ...model.BranchTag) []model.Commit {
	cs := make([]model.Commit, len(tags))
	for i, t := range tags {
		cs[i].Tag = t
	}
	return cs
}

func TestSegments_RunsAreCollapsed(t *testing.T) {
	cs := tagged(
		model.BranchTagAgainst,
		model.BranchTagAgainst,
		model.BranchTagFeature,
		model.BranchTagFeature,
		model.BranchTagFeature,
		model.BranchTagAgainst,
	)
	got := Segments(cs)
	want := []Segment{
		{Tag: model.BranchTagAgainst, Start: 0, End: 1},
		{Tag: model.BranchTagFeature, Start: 2, End: 4},
		{Tag: model.BranchTagAgainst, Start: 5, End: 5},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("segments = %+v, want %+v", got, want)
	}
	if got[1].Len() != 3 {
		t.Errorf("middle segment len = %d, want 3", got[1].Len())
	}
}

func TestSegments_Empty(t *testing.T) {
	if got := Segments(nil); got != nil {
		t.Errorf("expected nil for empty, got %+v", got)
	}
}

func TestSegments_Single(t *testing.T) {
	got := Segments(tagged(model.BranchTagFeature))
	if len(got) != 1 || got[0].Start != 0 || got[0].End != 0 {
		t.Errorf("got %+v", got)
	}
}
