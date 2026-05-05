package replay

import (
	"testing"

	"github.com/S-Nakamur-a/gitfilm/internal/model"
)

func TestHeatSnapshotWith_DropsBelowHiddenBelow(t *testing.T) {
	st := NewTreeState(0)
	// hot: 1000, cold: 1 → ratio 0.001, below HiddenBelow (0.005).
	st.Step(model.Commit{Files: []model.FileChange{
		{Path: "hot.go", Status: model.StatusAdded, Added: 1000},
		{Path: "cold.go", Status: model.StatusAdded, Added: 1},
	}})

	hs := st.HeatSnapshotWith(SnapshotOpts{HiddenBelow: 0.005})
	if _, ok := hs.Heat["hot.go"]; !ok {
		t.Errorf("hot.go should be retained")
	}
	if _, ok := hs.Heat["cold.go"]; ok {
		t.Errorf("cold.go (ratio 0.001) should be filtered out")
	}
	// Touches must follow Heat: dropped paths carry no touches either.
	if _, ok := hs.Touches["cold.go"]; ok {
		t.Errorf("cold.go touches should be filtered with heat")
	}
}

func TestHeatSnapshotWith_KeepsLukewarmAboveHiddenBelow(t *testing.T) {
	st := NewTreeState(0)
	// warm: 20 / 1000 = 0.02 — above HiddenBelow but below FaintBelow.
	st.Step(model.Commit{Files: []model.FileChange{
		{Path: "hot.go", Status: model.StatusAdded, Added: 1000},
		{Path: "warm.go", Status: model.StatusAdded, Added: 20},
	}})
	hs := st.HeatSnapshotWith(SnapshotOpts{HiddenBelow: 0.005, FaintBelow: 0.05})
	if _, ok := hs.Heat["warm.go"]; !ok {
		t.Errorf("warm.go (ratio 0.02) should be retained — FaintBelow must not affect filtering")
	}
}

func TestHeatSnapshotWith_DropsGhostWhenHeatBelowThreshold(t *testing.T) {
	st := NewTreeState(0)
	// removed.go gets a tiny initial heat then is deleted. With hot.go
	// dominating, removed.go's heat ratio is below HiddenBelow. Ghost
	// must be dropped or it would resurrect as a permanent 👻 row in
	// the JS branch that fires when a path is absent from heat.
	st.Step(model.Commit{Files: []model.FileChange{
		{Path: "hot.go", Status: model.StatusAdded, Added: 1000},
		{Path: "removed.go", Status: model.StatusAdded, Added: 1},
	}})
	st.Step(model.Commit{Files: []model.FileChange{
		{Path: "removed.go", Status: model.StatusDeleted, Removed: 0},
	}})

	hs := st.HeatSnapshotWith(SnapshotOpts{HiddenBelow: 0.005})
	if hs.Ghosts["removed.go"] {
		t.Errorf("ghost with below-threshold heat should be dropped to match unfiltered JS visibility")
	}
}

func TestHeatSnapshotWith_KeepsGhostWhenHeatPassesThreshold(t *testing.T) {
	st := NewTreeState(0)
	// removed.go has substantial heat then is deleted in the same frame
	// run. Ratio passes threshold → ghost must be kept (the JS will
	// render it via the heat row branch, which is fine).
	st.Step(model.Commit{Files: []model.FileChange{
		{Path: "removed.go", Status: model.StatusAdded, Added: 100},
	}})
	st.Step(model.Commit{Files: []model.FileChange{
		{Path: "removed.go", Status: model.StatusDeleted, Removed: 50},
	}})
	hs := st.HeatSnapshotWith(SnapshotOpts{HiddenBelow: 0.005})
	if !hs.Ghosts["removed.go"] {
		t.Errorf("ghost with above-threshold heat should be retained")
	}
}

func TestHeatSnapshotWith_ZeroHiddenBelowMatchesUnfiltered(t *testing.T) {
	st := NewTreeState(0)
	st.Step(model.Commit{Files: []model.FileChange{
		{Path: "a.go", Status: model.StatusAdded, Added: 100},
		{Path: "b.go", Status: model.StatusAdded, Added: 1},
	}})
	full := st.HeatSnapshot()
	filtered := st.HeatSnapshotWith(SnapshotOpts{HiddenBelow: 0})
	if len(full.Heat) != len(filtered.Heat) {
		t.Errorf("HiddenBelow=0 must behave as no-op: heat sizes %d vs %d", len(full.Heat), len(filtered.Heat))
	}
	for k, v := range full.Heat {
		if filtered.Heat[k] != v {
			t.Errorf("path %q: heat differs %f vs %f", k, v, filtered.Heat[k])
		}
	}
}
