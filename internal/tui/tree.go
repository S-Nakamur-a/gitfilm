package tui

import (
	"math"
	"sort"
	"strings"

	"github.com/S-Nakamur-a/gitplay/internal/model"
)

// TreeNode is a directory or file in the live filesystem view.
// Children are kept in a stable order (dirs first, then alphabetical).
type TreeNode struct {
	Name      string
	Path      string // full path from the repo root (or subdir)
	IsDir     bool
	Deleted   bool // for deleted files we keep a "ghost" entry briefly
	Touches   int  // commits that have touched this file up to current frame
	Heat      float64
	HeatRatio float64 // Heat / MaxHeat at snapshot time, useful for filtering
	Faint     bool    // true when heat is decayed but not yet hidden
	Status    model.ChangeStatus
	NewInThis bool // true if this file was added in the current frame
	Children  []*TreeNode
}

// SnapshotOpts controls visibility filtering when materializing a
// TreeState into a TreeNode tree. Files below HiddenBelow (relative
// to the current max heat) disappear; files between HiddenBelow and
// FaintBelow render in a dim style with no heat bar.
type SnapshotOpts struct {
	FaintBelow  float64 // dim threshold, 0..1
	HiddenBelow float64 // hide threshold, 0..1
}

// DefaultSnapshotOpts returns sensible thresholds for the live UI.
func DefaultSnapshotOpts() SnapshotOpts {
	return SnapshotOpts{FaintBelow: 0.05, HiddenBelow: 0.005}
}

// TreeState tracks per-file heat over time. Build it once, Step it per
// commit, then call Snapshot to materialize a TreeNode tree.
type TreeState struct {
	heat     map[string]float64
	touches  map[string]int
	deleted  map[string]bool // path -> still ghosting
	added    map[string]bool // freshly added in the current frame (cleared by next Step)
	statuses map[string]model.ChangeStatus

	halfLife float64
	decay    float64
}

// Clone returns an independent copy of the TreeState. Used by the TUI
// to cache periodic snapshots so backward navigation doesn't have to
// replay history from the very beginning each time.
func (t *TreeState) Clone() *TreeState {
	c := &TreeState{
		heat:     make(map[string]float64, len(t.heat)),
		touches:  make(map[string]int, len(t.touches)),
		deleted:  make(map[string]bool, len(t.deleted)),
		added:    make(map[string]bool, len(t.added)),
		statuses: make(map[string]model.ChangeStatus, len(t.statuses)),
		halfLife: t.halfLife,
		decay:    t.decay,
	}
	for k, v := range t.heat {
		c.heat[k] = v
	}
	for k, v := range t.touches {
		c.touches[k] = v
	}
	for k, v := range t.deleted {
		c.deleted[k] = v
	}
	for k, v := range t.added {
		c.added[k] = v
	}
	for k, v := range t.statuses {
		c.statuses[k] = v
	}
	return c
}

func NewTreeState(halfLife float64) *TreeState {
	t := &TreeState{
		heat:     make(map[string]float64),
		touches:  make(map[string]int),
		deleted:  make(map[string]bool),
		added:    make(map[string]bool),
		statuses: make(map[string]model.ChangeStatus),
		halfLife: halfLife,
	}
	if halfLife > 0 {
		t.decay = math.Pow(0.5, 1.0/halfLife)
	} else {
		t.decay = 1.0
	}
	return t
}

// Step advances state by one commit.
func (t *TreeState) Step(c model.Commit) {
	if t.halfLife > 0 {
		for k := range t.heat {
			t.heat[k] *= t.decay
		}
	}
	// freshly-added flags reset every frame
	t.added = make(map[string]bool)

	for _, f := range c.Files {
		if f.Status == model.StatusRenamed && f.OldPath != "" && f.OldPath != f.Path {
			t.heat[f.Path] += t.heat[f.OldPath]
			t.touches[f.Path] += t.touches[f.OldPath]
			delete(t.heat, f.OldPath)
			delete(t.touches, f.OldPath)
			delete(t.statuses, f.OldPath)
		}
		t.heat[f.Path] += float64(f.Added + f.Removed)
		t.touches[f.Path]++
		t.statuses[f.Path] = f.Status
		switch f.Status {
		case model.StatusAdded:
			t.added[f.Path] = true
			delete(t.deleted, f.Path)
		case model.StatusDeleted:
			t.deleted[f.Path] = true
		default:
			delete(t.deleted, f.Path)
		}
	}
}

// Snapshot materializes the current state into a tree rooted at "",
// with default visibility filtering.
func (t *TreeState) Snapshot() *TreeNode {
	return t.SnapshotWith(DefaultSnapshotOpts())
}

// SnapshotWith materializes the current state, applying the given
// visibility thresholds. Files whose heat ratio falls below
// HiddenBelow are dropped entirely; deleted (ghost) files are always
// kept short-term because the deletion event itself is information.
//
// Empty directories that result from filtering are pruned, so the
// tree naturally collapses around active areas.
func (t *TreeState) SnapshotWith(opts SnapshotOpts) *TreeNode {
	max := 0.0
	for _, v := range t.heat {
		if v > max {
			max = v
		}
	}
	if max <= 0 {
		max = 1
	}
	root := &TreeNode{Name: "", Path: "", IsDir: true}
	for path := range t.touches {
		if t.deleted[path] {
			continue
		}
		heat := t.heat[path]
		ratio := heat / max
		if ratio < opts.HiddenBelow {
			continue
		}
		insert(root, path, &TreeNode{
			Name:      base(path),
			Path:      path,
			IsDir:     false,
			Touches:   t.touches[path],
			Heat:      heat,
			HeatRatio: ratio,
			Faint:     ratio < opts.FaintBelow,
			Status:    t.statuses[path],
			NewInThis: t.added[path],
		})
	}
	for path := range t.deleted {
		insert(root, path, &TreeNode{
			Name:    base(path),
			Path:    path,
			IsDir:   false,
			Deleted: true,
			Touches: t.touches[path],
			Status:  model.StatusDeleted,
		})
	}
	sortTree(root)
	pruneEmptyDirs(root)
	return root
}

// pruneEmptyDirs removes directory nodes whose entire subtree is
// empty (i.e. all leaves were filtered out by visibility thresholds).
// Returns true if the node passed in is itself prunable.
func pruneEmptyDirs(n *TreeNode) bool {
	if !n.IsDir {
		return false
	}
	kept := n.Children[:0]
	for _, c := range n.Children {
		if c.IsDir && pruneEmptyDirs(c) {
			continue
		}
		kept = append(kept, c)
	}
	n.Children = kept
	return n.IsDir && len(n.Children) == 0
}

func insert(root *TreeNode, path string, leaf *TreeNode) {
	parts := strings.Split(path, "/")
	cur := root
	for i, p := range parts {
		if i == len(parts)-1 {
			cur.Children = append(cur.Children, leaf)
			return
		}
		var next *TreeNode
		for _, c := range cur.Children {
			if c.IsDir && c.Name == p {
				next = c
				break
			}
		}
		if next == nil {
			next = &TreeNode{
				Name:  p,
				Path:  strings.Join(parts[:i+1], "/"),
				IsDir: true,
			}
			cur.Children = append(cur.Children, next)
		}
		cur = next
	}
}

func sortTree(n *TreeNode) {
	sort.SliceStable(n.Children, func(i, j int) bool {
		a, b := n.Children[i], n.Children[j]
		if a.IsDir != b.IsDir {
			return a.IsDir // dirs first
		}
		return a.Name < b.Name
	})
	for _, c := range n.Children {
		if c.IsDir {
			sortTree(c)
		}
	}
}

func base(path string) string {
	if i := strings.LastIndexByte(path, '/'); i >= 0 {
		return path[i+1:]
	}
	return path
}
