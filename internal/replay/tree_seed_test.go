package replay

import (
	"testing"

	"github.com/S-Nakamur-a/gitfilm/internal/model"
)

// findCold searches the tree for a cold-aware match: real cold leaves,
// cold dir placeholders (CollapsedCount > 0 && IsDir), and cold
// sibling-files placeholders (CollapsedCount > 0 && !IsDir). Path is
// matched on the dir name for placeholders.
func findByName(n *TreeNode, name string) *TreeNode {
	if n.Name == name {
		return n
	}
	for _, c := range n.Children {
		if got := findByName(c, name); got != nil {
			return got
		}
	}
	return nil
}

func TestSeed_ShowsExistingFilesAsCold(t *testing.T) {
	st := NewTreeState(0)
	st.Seed([]string{"README.md", "internal/replay/tree.go"})
	// One hot file inside internal/replay so the dir survives as hot,
	// and a wholly cold subtree (README.md sits at root cold).
	st.Step(model.Commit{Files: []model.FileChange{
		{Path: "internal/replay/tree.go", Status: model.StatusModified, Added: 1000},
	}})
	root := st.SnapshotWith(SnapshotOpts{FaintBelow: 0.05, HiddenBelow: 0.005})

	// README.md is purely seeded (no touches) and at the root level;
	// since it is the lone cold file at root and main is the hot
	// dir, README should appear as the aggregated "…(N more files)"
	// placeholder rather than its real name.
	files := map[string]*TreeNode{}
	collectFiles(root, files)
	if got := files["README.md"]; got != nil {
		t.Errorf("README.md should have been aggregated into a cold sibling placeholder; got %+v", got)
	}
	// Find the placeholder that aggregates root-level cold leaves.
	var rootColdAgg *TreeNode
	for _, c := range root.Children {
		if !c.IsDir && c.CollapsedCount > 0 {
			rootColdAgg = c
			break
		}
	}
	if rootColdAgg == nil {
		t.Fatalf("expected a root-level cold sibling placeholder")
	}
	if rootColdAgg.CollapsedCount != 1 {
		t.Errorf("root cold placeholder should aggregate 1 file (README.md), got CollapsedCount=%d", rootColdAgg.CollapsedCount)
	}
}

func TestSeed_ColdSubtreeCollapsesToSingleRow(t *testing.T) {
	st := NewTreeState(0)
	// Seed three files in vendor/ that are never touched, plus a hot
	// file at root.
	st.Seed([]string{
		"vendor/a/x.go",
		"vendor/a/y.go",
		"vendor/b/z.go",
	})
	st.Step(model.Commit{Files: []model.FileChange{
		{Path: "main.go", Status: model.StatusAdded, Added: 1000},
	}})
	root := st.SnapshotWith(SnapshotOpts{FaintBelow: 0.05, HiddenBelow: 0.005})

	// vendor/ should appear as a single placeholder row, not as a
	// recursed subtree exposing a/ and b/.
	vendor := findByName(root, "vendor")
	if vendor == nil {
		t.Fatalf("vendor placeholder missing")
	}
	if !vendor.IsDir || vendor.CollapsedCount == 0 {
		t.Fatalf("vendor should be a dir-style placeholder, got %+v", vendor)
	}
	if vendor.CollapsedCount != 3 {
		t.Errorf("vendor placeholder should report 3 files, got %d", vendor.CollapsedCount)
	}
	if len(vendor.Children) != 0 {
		t.Errorf("vendor placeholder must not have children, got %d", len(vendor.Children))
	}
}

func TestSeed_HotDirAggregatesColdSiblings(t *testing.T) {
	st := NewTreeState(0)
	// One hot file inside pkg/, plus four cold seeded siblings in
	// the same directory. Expect the hot file shown + a single
	// "…(4 more files)" placeholder, not four cold leaves.
	st.Seed([]string{
		"pkg/cold1.go",
		"pkg/cold2.go",
		"pkg/cold3.go",
		"pkg/cold4.go",
	})
	st.Step(model.Commit{Files: []model.FileChange{
		{Path: "pkg/hot.go", Status: model.StatusAdded, Added: 1000},
	}})
	root := st.SnapshotWith(SnapshotOpts{FaintBelow: 0.05, HiddenBelow: 0.005})

	pkg := findByName(root, "pkg")
	if pkg == nil || !pkg.IsDir || pkg.CollapsedCount != 0 {
		t.Fatalf("pkg should remain a real (hot) dir, got %+v", pkg)
	}
	var coldFiles, hotFiles, placeholders int
	for _, c := range pkg.Children {
		switch {
		case c.CollapsedCount > 0 && !c.IsDir:
			placeholders++
			if c.CollapsedCount != 4 {
				t.Errorf("placeholder should aggregate 4 cold siblings, got %d", c.CollapsedCount)
			}
		case c.Cold:
			coldFiles++
		default:
			hotFiles++
		}
	}
	if hotFiles != 1 {
		t.Errorf("expected exactly 1 hot file under pkg/, got %d", hotFiles)
	}
	if placeholders != 1 {
		t.Errorf("expected exactly 1 cold sibling placeholder under pkg/, got %d", placeholders)
	}
	if coldFiles != 0 {
		t.Errorf("cold leaves should have been aggregated, got %d standalone cold files", coldFiles)
	}
}

func TestSeed_DeletedRemovesFromExisting(t *testing.T) {
	st := NewTreeState(0)
	st.Seed([]string{"obsolete.go", "main.go"})
	st.Step(model.Commit{Files: []model.FileChange{
		{Path: "main.go", Status: model.StatusModified, Added: 1000},
	}})
	// Delete obsolete.go in a later commit. The seed entry for it
	// must NOT resurrect the path as a cold row — the deletion is
	// expected to also drop it from t.existing so it doesn't leak
	// back through the seeded-path branch in SnapshotWith.
	st.Step(model.Commit{Files: []model.FileChange{
		{Path: "obsolete.go", Status: model.StatusDeleted, Removed: 1},
	}})
	root := st.SnapshotWith(SnapshotOpts{FaintBelow: 0.05, HiddenBelow: 0.005})

	if findByName(root, "obsolete.go") != nil {
		t.Errorf("deleted seed path must not survive in tree (state should clear t.existing)")
	}
	if findByName(root, "main.go") == nil {
		t.Errorf("non-deleted seed path must remain")
	}
}

func TestSeed_ClonePreservesSeed(t *testing.T) {
	st := NewTreeState(0)
	st.Seed([]string{"a.go", "b.go"})
	c := st.Clone()
	if !c.existing["a.go"] || !c.existing["b.go"] {
		t.Errorf("clone should preserve seeded paths, got existing=%+v", c.existing)
	}
	// Mutating original should not affect clone.
	st.Seed([]string{"c.go"})
	if c.existing["c.go"] {
		t.Errorf("clone should be independent of subsequent Seed calls on the original")
	}
}
