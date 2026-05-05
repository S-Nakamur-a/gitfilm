package replay

import (
	"math"
	"sort"
	"strings"

	"github.com/S-Nakamur-a/gitfilm/internal/model"
)

// DefaultHalfLife is the heat half-life (in commits) used by the live
// UI. A commit's churn contribution loses half its weight every N
// commits, so the heat-map drifts toward whatever is *recently* hot.
const DefaultHalfLife = 7.0

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
		insertNode(root, path, &TreeNode{
			Name:      basePath(path),
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
		insertNode(root, path, &TreeNode{
			Name:    basePath(path),
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

// HeatSnapshot is a renderer-agnostic, JSON-friendly view of the
// per-frame heat map. Used by the HTML output to ship precomputed
// snapshots so the browser doesn't reimplement heat decay.
type HeatSnapshot struct {
	// Heat is path -> raw heat (post-decay, pre-normalization).
	Heat map[string]float64
	// Touches is path -> commits that have touched this path so far.
	Touches map[string]int
	// Ghosts is the set of paths currently in the deleted/ghost state.
	Ghosts map[string]bool
	// Added is the set of paths added in the most recent step.
	Added map[string]bool
}

// HeatSnapshot returns a flat copy of the live state suitable for JSON
// serialization. Cheaper than SnapshotWith because no filtering or
// directory tree is built.
func (t *TreeState) HeatSnapshot() HeatSnapshot {
	hs := HeatSnapshot{
		Heat:    make(map[string]float64, len(t.heat)),
		Touches: make(map[string]int, len(t.touches)),
		Ghosts:  make(map[string]bool, len(t.deleted)),
		Added:   make(map[string]bool, len(t.added)),
	}
	for k, v := range t.heat {
		hs.Heat[k] = v
	}
	for k, v := range t.touches {
		hs.Touches[k] = v
	}
	for k, v := range t.deleted {
		if v {
			hs.Ghosts[k] = true
		}
	}
	for k, v := range t.added {
		if v {
			hs.Added[k] = true
		}
	}
	return hs
}

// HeatSnapshotWith is HeatSnapshot but skips paths whose heat ratio
// (heat / current max heat) falls below opts.HiddenBelow. This matches
// the visibility rule the HTML player applies on the browser side, so
// the precomputed JSON does not carry rows that would be hidden anyway.
//
// Why this matters for big repos: the unfiltered snapshot retains every
// path ever touched (Step decays heat but never deletes the entry), so
// per-frame size grows with cumulative-unique-files. On a multi-year
// monorepo with `--max 0` that pushed the HTML payload to tens of GB,
// because each of N commits carries the full path×heat map of every
// file. Filtering at source bounds each frame to recently-active files.
//
// FaintBelow is intentionally ignored — faint files are still visible
// in the player and must be retained.
func (t *TreeState) HeatSnapshotWith(opts SnapshotOpts) HeatSnapshot {
	if opts.HiddenBelow <= 0 {
		return t.HeatSnapshot()
	}
	max := 0.0
	for _, v := range t.heat {
		if v > max {
			max = v
		}
	}
	if max <= 0 {
		max = 1
	}
	threshold := opts.HiddenBelow * max

	hs := HeatSnapshot{
		Heat:    make(map[string]float64),
		Touches: make(map[string]int),
		Ghosts:  make(map[string]bool),
		Added:   make(map[string]bool, len(t.added)),
	}
	for k, v := range t.heat {
		if v < threshold {
			continue
		}
		hs.Heat[k] = v
		hs.Touches[k] = t.touches[k]
	}
	// Mirror the player's ghost branch: it only fires for paths absent
	// from the heat map. Step never removes from heat, so unfiltered
	// runs effectively never use that branch — a ghost whose heat is
	// below threshold gets dropped via the ratio check instead. We must
	// drop those ghosts too, otherwise filtering would resurrect them as
	// permanent 👻 rows.
	for k, v := range t.deleted {
		if !v {
			continue
		}
		if h, ok := t.heat[k]; ok && h < threshold {
			continue
		}
		hs.Ghosts[k] = true
	}
	for k, v := range t.added {
		if v {
			hs.Added[k] = true
		}
	}
	return hs
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

func insertNode(root *TreeNode, path string, leaf *TreeNode) {
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

func basePath(path string) string {
	if i := strings.LastIndexByte(path, '/'); i >= 0 {
		return path[i+1:]
	}
	return path
}
