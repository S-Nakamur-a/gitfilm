package replay

import (
	"testing"

	"github.com/S-Nakamur-a/gitfilm/internal/model"
)

func find(n *TreeNode, path string) *TreeNode {
	if n.Path == path && !n.IsDir {
		return n
	}
	for _, c := range n.Children {
		if got := find(c, path); got != nil {
			return got
		}
	}
	return nil
}

func TestTreeState_BuildsHierarchy(t *testing.T) {
	st := NewTreeState(0)
	st.Step(model.Commit{Files: []model.FileChange{
		{Path: "src/a.go", Status: model.StatusAdded, Added: 10},
		{Path: "src/sub/b.go", Status: model.StatusAdded, Added: 5},
		{Path: "README.md", Status: model.StatusAdded, Added: 3},
	}})
	root := st.Snapshot()
	// Expect: dirs first (src/), then file README.md.
	if len(root.Children) != 2 {
		t.Fatalf("root children = %d, want 2: %+v", len(root.Children), root.Children)
	}
	if !root.Children[0].IsDir || root.Children[0].Name != "src" {
		t.Errorf("first child should be dir 'src', got %+v", root.Children[0])
	}
	if root.Children[1].Name != "README.md" {
		t.Errorf("second child should be README.md, got %s", root.Children[1].Name)
	}
	a := find(root, "src/a.go")
	if a == nil || a.Touches != 1 || a.Heat != 10 || !a.NewInThis {
		t.Errorf("src/a.go = %+v", a)
	}
}

func TestTreeState_DeletedFileBecomesGhost(t *testing.T) {
	st := NewTreeState(0)
	st.Step(model.Commit{Files: []model.FileChange{
		{Path: "old.go", Status: model.StatusAdded, Added: 4},
	}})
	st.Step(model.Commit{Files: []model.FileChange{
		{Path: "old.go", Status: model.StatusDeleted, Removed: 4},
	}})
	root := st.Snapshot()
	g := find(root, "old.go")
	if g == nil {
		t.Fatalf("ghost not present")
	}
	if !g.Deleted {
		t.Errorf("expected Deleted=true, got %+v", g)
	}
}

func TestTreeState_RenameMovesEntry(t *testing.T) {
	st := NewTreeState(0)
	st.Step(model.Commit{Files: []model.FileChange{
		{Path: "old.go", Status: model.StatusAdded, Added: 50},
	}})
	st.Step(model.Commit{Files: []model.FileChange{
		{Path: "new.go", OldPath: "old.go", Status: model.StatusRenamed, Added: 1},
	}})
	root := st.Snapshot()
	if find(root, "old.go") != nil {
		t.Errorf("old.go should be gone after rename")
	}
	n := find(root, "new.go")
	if n == nil || n.Heat != 51 || n.Touches != 2 {
		t.Errorf("new.go = %+v", n)
	}
}

func TestTreeState_NewInThisOnlyForLatestFrame(t *testing.T) {
	st := NewTreeState(0)
	st.Step(model.Commit{Files: []model.FileChange{
		{Path: "a.go", Status: model.StatusAdded, Added: 1},
	}})
	if find(st.Snapshot(), "a.go").NewInThis != true {
		t.Errorf("expected NewInThis on add frame")
	}
	st.Step(model.Commit{Files: []model.FileChange{
		{Path: "a.go", Status: model.StatusModified, Added: 2, Removed: 1},
	}})
	if find(st.Snapshot(), "a.go").NewInThis != false {
		t.Errorf("NewInThis should reset after a non-add step")
	}
}
