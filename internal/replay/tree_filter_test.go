package replay

import (
	"testing"

	"github.com/S-Nakamur-a/gitfilm/internal/model"
)

func collectFiles(n *TreeNode, into map[string]*TreeNode) {
	if !n.IsDir && n.Path != "" {
		into[n.Path] = n
	}
	for _, c := range n.Children {
		collectFiles(c, into)
	}
}

func TestSnapshotWith_HidesColdFiles(t *testing.T) {
	st := NewTreeState(0)
	// Hot file: 1000 churn. Cold file: 1 churn. Ratio = 0.001 → below
	// HiddenBelow (0.005), should be dropped.
	st.Step(model.Commit{Files: []model.FileChange{
		{Path: "hot.go", Status: model.StatusAdded, Added: 1000},
		{Path: "cold.go", Status: model.StatusAdded, Added: 1},
	}})

	root := st.SnapshotWith(SnapshotOpts{FaintBelow: 0.05, HiddenBelow: 0.005})
	files := map[string]*TreeNode{}
	collectFiles(root, files)
	if files["hot.go"] == nil {
		t.Errorf("hot file should be visible")
	}
	if files["cold.go"] != nil {
		t.Errorf("cold file (ratio 0.001) should be hidden, got %+v", files["cold.go"])
	}
}

func TestSnapshotWith_FaintForLukewarmFiles(t *testing.T) {
	st := NewTreeState(0)
	// hot: 1000, lukewarm: 20 → ratio 0.02. Between HiddenBelow (0.005)
	// and FaintBelow (0.05), so it should be visible-but-faint.
	st.Step(model.Commit{Files: []model.FileChange{
		{Path: "hot.go", Status: model.StatusAdded, Added: 1000},
		{Path: "warm.go", Status: model.StatusAdded, Added: 20},
	}})
	root := st.SnapshotWith(SnapshotOpts{FaintBelow: 0.05, HiddenBelow: 0.005})
	files := map[string]*TreeNode{}
	collectFiles(root, files)
	w := files["warm.go"]
	if w == nil {
		t.Fatalf("warm file should still be visible")
	}
	if !w.Faint {
		t.Errorf("warm file should be faint, got Faint=%v", w.Faint)
	}
	h := files["hot.go"]
	if h == nil || h.Faint {
		t.Errorf("hot file should be visible and not faint, got %+v", h)
	}
}

func TestSnapshotWith_DeletedFileVanishes(t *testing.T) {
	// Deletion is now an instant disappearance — the path should not
	// be in the rendered tree at all, regardless of how much heat its
	// final commit added. Under the old "ghost" model this rendered
	// as a 👻 row that hung around for many frames. The state map
	// (t.deleted) still tracks the path internally so the touched-walk
	// can filter it; that's why we also assert the sibling stays.
	st := NewTreeState(0)
	st.Step(model.Commit{Files: []model.FileChange{
		{Path: "kept.go", Status: model.StatusAdded, Added: 100},
		{Path: "removed.go", Status: model.StatusAdded, Added: 100},
	}})
	st.Step(model.Commit{Files: []model.FileChange{
		{Path: "removed.go", Status: model.StatusDeleted, Removed: 50},
	}})
	root := st.SnapshotWith(SnapshotOpts{FaintBelow: 0.05, HiddenBelow: 0.005})
	files := map[string]*TreeNode{}
	collectFiles(root, files)
	if files["removed.go"] != nil {
		t.Errorf("deleted file should not appear in tree, got %+v", files["removed.go"])
	}
	if files["kept.go"] == nil {
		t.Errorf("sibling file must remain visible")
	}
}

func TestSnapshotWith_PrunesEmptyDirectories(t *testing.T) {
	st := NewTreeState(0)
	// Two cold files in deep directories, plus one hot file at root.
	st.Step(model.Commit{Files: []model.FileChange{
		{Path: "src/a.go", Status: model.StatusAdded, Added: 1},
		{Path: "deep/sub/b.go", Status: model.StatusAdded, Added: 1},
		{Path: "main.go", Status: model.StatusAdded, Added: 1000},
	}})
	root := st.SnapshotWith(SnapshotOpts{FaintBelow: 0.05, HiddenBelow: 0.005})
	// "src/" and "deep/" have only cold descendants — should be pruned.
	for _, c := range root.Children {
		if c.IsDir && (c.Name == "src" || c.Name == "deep") {
			t.Errorf("dir %q should be pruned (no visible descendants)", c.Name)
		}
	}
	// main.go should remain
	files := map[string]*TreeNode{}
	collectFiles(root, files)
	if files["main.go"] == nil {
		t.Errorf("main.go should be visible")
	}
}
