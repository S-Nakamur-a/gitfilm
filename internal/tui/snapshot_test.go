package tui

import (
	"reflect"
	"testing"

	"github.com/S-Nakamur-a/gitplay/internal/model"
)

func TestTreeState_CloneIsIndependent(t *testing.T) {
	st := NewTreeState(0)
	st.Step(model.Commit{Files: []model.FileChange{
		{Path: "a.go", Status: model.StatusAdded, Added: 5},
	}})
	clone := st.Clone()

	// Mutating the original must not bleed into the clone.
	st.Step(model.Commit{Files: []model.FileChange{
		{Path: "b.go", Status: model.StatusAdded, Added: 3},
	}})
	if find(clone.Snapshot(), "b.go") != nil {
		t.Errorf("clone should not see post-clone steps")
	}
	if find(clone.Snapshot(), "a.go") == nil {
		t.Errorf("clone should preserve pre-clone state")
	}
}

func TestTreeState_CloneEqualsReplay(t *testing.T) {
	commits := []model.Commit{
		{Files: []model.FileChange{{Path: "a.go", Status: model.StatusAdded, Added: 5}}},
		{Files: []model.FileChange{{Path: "b.go", Status: model.StatusAdded, Added: 10}}},
		{Files: []model.FileChange{{Path: "a.go", Status: model.StatusModified, Added: 2, Removed: 1}}},
	}
	full := NewTreeState(0)
	for _, c := range commits {
		full.Step(c)
	}
	// Clone after step 1, then continue stepping the clone through 2 and 3.
	half := NewTreeState(0)
	half.Step(commits[0])
	clone := half.Clone()
	clone.Step(commits[1])
	clone.Step(commits[2])

	want := full.Snapshot()
	got := clone.Snapshot()
	if !reflect.DeepEqual(asMap(want), asMap(got)) {
		t.Errorf("clone+replay diverged from full replay")
	}
}

// asMap flattens a TreeNode tree into path -> heat for comparison.
func asMap(n *TreeNode) map[string]float64 {
	m := make(map[string]float64)
	var walk func(*TreeNode)
	walk = func(node *TreeNode) {
		if !node.IsDir && node.Path != "" {
			m[node.Path] = node.Heat
		}
		for _, c := range node.Children {
			walk(c)
		}
	}
	walk(n)
	return m
}
