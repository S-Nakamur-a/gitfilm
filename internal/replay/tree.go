package replay

import (
	"maps"
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
//
// Three kinds of nodes coexist in the tree:
//
//  1. Real file (IsDir=false, CollapsedCount=0): rendered with a heat
//     tier color (or Faint / Cold for cooler ones).
//  2. Real directory (IsDir=true, CollapsedCount=0): expanded; its
//     Children are rendered indented underneath.
//  3. Placeholder (CollapsedCount>0): a "summary" row that stands in
//     for one or more cold descendants. It is never recursed into;
//     IsDir indicates *what kind* of thing it summarizes (a cold
//     subtree if true, a bag of cold sibling files if false).
type TreeNode struct {
	Name      string
	Path      string // full path from the repo root (or subdir)
	IsDir     bool
	Touches   int // commits that have touched this file up to current frame
	Heat      float64
	HeatRatio float64 // Heat / MaxHeat at snapshot time, useful for filtering
	Faint     bool    // true when heat is decayed but not yet hidden
	// Cold marks "exists at this commit but heat is below the hidden
	// threshold". Cold nodes are kept in the tree for context (so the
	// user sees that a file or dir is still there) but rendered in a
	// neutral, dim style with no heat color or marker. A cold leaf
	// only appears when its path was seeded via Seed() — files added
	// during the load window that subsequently decayed are also
	// retained as cold so the user does not see them disappear.
	Cold bool
	// CollapsedCount > 0 marks this node as a placeholder for N cold
	// items that were folded out of view to keep the pane uncluttered.
	// IsDir tells the renderer which template to use:
	//
	//   - IsDir=true:  "<name>/  …(N files)" — a cold subtree
	//   - IsDir=false: "…(N more files)"     — a bag of cold siblings
	//
	// Placeholders never have Children; they are leaves in the
	// rendered sense even when IsDir is true.
	CollapsedCount int
	Status         model.ChangeStatus
	NewInThis      bool // true if this file was added in the current frame
	Children       []*TreeNode
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
	heat    map[string]float64
	touches map[string]int
	// deleted marks paths that have received a StatusDeleted event.
	// They never render (deletion = instant disappearance) but the
	// set is consulted by the touched-walk in SnapshotWith so the
	// path's leftover heat/touches entries don't inject it back as a
	// hot row. Re-adding the same path (StatusAdded) clears the bit.
	deleted  map[string]bool
	added    map[string]bool // freshly added in the current frame (cleared by next Step)
	statuses map[string]model.ChangeStatus

	// existing is the set of paths that already lived in the working
	// tree before the loaded window's first commit. Seeded once via
	// Seed() (typically from `git ls-tree` against the parent of the
	// oldest loaded commit), it lets the snapshot show those paths as
	// cold context even when they receive zero touches in the window.
	// Without this seed a `--max 100` view would show only the files
	// whose history happened to fall inside the window — i.e. tiny
	// fragments of the actual repo with no surrounding structure.
	//
	// existing has no effect on heat math; it is purely a "candidate
	// set" extension consulted at Snapshot time.
	existing map[string]bool

	// loc is the cumulative net line count per file (added − removed
	// across all commits applied so far). Heat decays so it can't be
	// used as a "size" measure for the treemap; loc is monotonic-ish
	// (only changes from explicit diffs) and represents "how big is
	// this file right now". Cleared on StatusDeleted; transferred on
	// StatusRenamed.
	loc map[string]int

	// Cumulative totals across all commits applied so far. These are
	// monotonic (Step only adds) so the running-counters HUD can
	// read them in O(1) without rewalking history. Snapshots/Clones
	// preserve them so backward navigation lands on the right total.
	totalAdded   int
	totalRemoved int
	totalCommits int

	halfLife float64
	decay    float64
}

// Clone returns an independent copy of the TreeState. Used by the TUI
// to cache periodic snapshots so backward navigation doesn't have to
// replay history from the very beginning each time.
func (t *TreeState) Clone() *TreeState {
	return &TreeState{
		heat:         maps.Clone(t.heat),
		touches:      maps.Clone(t.touches),
		deleted:      maps.Clone(t.deleted),
		added:        maps.Clone(t.added),
		statuses:     maps.Clone(t.statuses),
		loc:          maps.Clone(t.loc),
		existing:     maps.Clone(t.existing),
		totalAdded:   t.totalAdded,
		totalRemoved: t.totalRemoved,
		totalCommits: t.totalCommits,
		halfLife:     t.halfLife,
		decay:        t.decay,
	}
}

func NewTreeState(halfLife float64) *TreeState {
	t := &TreeState{
		heat:     make(map[string]float64),
		touches:  make(map[string]int),
		deleted:  make(map[string]bool),
		added:    make(map[string]bool),
		statuses: make(map[string]model.ChangeStatus),
		loc:      make(map[string]int),
		existing: make(map[string]bool),
		halfLife: halfLife,
	}
	if halfLife > 0 {
		t.decay = math.Pow(0.5, 1.0/halfLife)
	} else {
		t.decay = 1.0
	}
	return t
}

// Seed marks the given paths as "already existing at this commit",
// without contributing any heat or touches. The snapshot will render
// them as cold context rows so the user sees the surrounding repo
// structure even when those files are not modified inside the loaded
// window. Safe to call multiple times; later calls accumulate.
//
// Paths added later via Step (StatusAdded) are independently tracked
// in `touches` and do not need to be seeded.
func (t *TreeState) Seed(paths []string) {
	for _, p := range paths {
		if p == "" {
			continue
		}
		t.existing[p] = true
	}
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
	t.totalCommits++

	for _, f := range c.Files {
		if f.Status == model.StatusRenamed && f.OldPath != "" && f.OldPath != f.Path {
			t.heat[f.Path] += t.heat[f.OldPath]
			t.touches[f.Path] += t.touches[f.OldPath]
			t.loc[f.Path] += t.loc[f.OldPath]
			if t.existing[f.OldPath] {
				t.existing[f.Path] = true
				delete(t.existing, f.OldPath)
			}
			delete(t.heat, f.OldPath)
			delete(t.touches, f.OldPath)
			delete(t.statuses, f.OldPath)
			delete(t.loc, f.OldPath)
		}
		t.heat[f.Path] += float64(f.Added + f.Removed)
		t.touches[f.Path]++
		t.statuses[f.Path] = f.Status
		t.totalAdded += f.Added
		t.totalRemoved += f.Removed
		// Net LOC for this file. Floor at 0 because git diffs don't
		// always reach absolute LOC=0 on heavy refactors and a
		// negative value would skew the treemap weights.
		t.loc[f.Path] += f.Added - f.Removed
		if t.loc[f.Path] < 0 {
			t.loc[f.Path] = 0
		}
		switch f.Status {
		case model.StatusAdded:
			t.added[f.Path] = true
			delete(t.deleted, f.Path)
		case model.StatusDeleted:
			t.deleted[f.Path] = true
			delete(t.loc, f.Path)
			// Drop from the seed too: a deleted path no longer
			// "exists at this commit" and must not resurrect via the
			// seeded-path branch in SnapshotWith.
			delete(t.existing, f.Path)
		default:
			delete(t.deleted, f.Path)
		}
	}
}

// LOCSnapshot returns a copy of the per-file net line counts. Used
// by the treemap renderer as the size weight for each rectangle.
// Returns a fresh map; callers can mutate freely without affecting
// the underlying state.
func (t *TreeState) LOCSnapshot() map[string]int {
	return maps.Clone(t.loc)
}

// HeatOf returns the post-decay heat for one path. Renderers use it
// to color a single cell without materializing the full snapshot.
// O(1).
func (t *TreeState) HeatOf(path string) float64 { return t.heat[path] }

// MaxHeat returns the largest current heat across all paths, used
// by callers to normalize heat into a 0..1 ratio. Returns 0 when
// the state is empty (callers should avoid divide-by-zero).
// O(N) — call sparingly (once per frame, not per cell).
func (t *TreeState) MaxHeat() float64 {
	maxH := 0.0
	for _, v := range t.heat {
		if v > maxH {
			maxH = v
		}
	}
	return maxH
}

// maxHeatFloor returns MaxHeat with a 1.0 floor so callers can divide
// safely without an extra zero check. Internal helper used by snapshot
// builders that need a normalization denominator.
func (t *TreeState) maxHeatFloor() float64 {
	if m := t.MaxHeat(); m > 0 {
		return m
	}
	return 1
}

// Counts is a small read-only snapshot of TreeState's cumulative
// totals. Renderers use it for "running counters" HUDs without
// reaching into private fields.
type Counts struct {
	Added       int // sum of FileChange.Added across all stepped commits
	Removed     int // sum of FileChange.Removed
	UniqueFiles int // distinct paths touched (post-rename consolidation)
	Commits     int // commits stepped
}

// Counts returns the cumulative totals up to the most recent Step.
// O(1); does not allocate.
func (t *TreeState) Counts() Counts {
	return Counts{
		Added:       t.totalAdded,
		Removed:     t.totalRemoved,
		UniqueFiles: len(t.touches),
		Commits:     t.totalCommits,
	}
}

// Snapshot materializes the current state into a tree rooted at "",
// with default visibility filtering.
func (t *TreeState) Snapshot() *TreeNode {
	return t.SnapshotWith(DefaultSnapshotOpts())
}

// SnapshotWith materializes the current state, applying the given
// visibility thresholds.
//
// Candidate paths are the union of "ever touched in the window" and
// "seeded as existing before the window". Of those:
//
//   - heat ratio >= FaintBelow → rendered with a heat tier color.
//   - HiddenBelow ≤ ratio < FaintBelow → marked Faint (still a real
//     row, but dim).
//   - ratio < HiddenBelow → marked Cold; collapsed by collapseCold
//     into a single placeholder row per dir / sibling group so the
//     pane stays readable on big repos.
//
// Cold paths that were neither seeded nor touched are dropped (they
// would carry no signal — only the seed gives us a reason to keep
// long-cold paths visible). Deleted paths are filtered out at every
// stage: they vanish from the rendered tree the instant their commit
// applies. Their `t.deleted` membership is still tracked so the
// touched-walk's `seen` filter can skip them — without that, the
// path's leftover heat/touches entries would inject the path back as
// a normal hot row.
func (t *TreeState) SnapshotWith(opts SnapshotOpts) *TreeNode {
	maxH := t.maxHeatFloor()
	root := &TreeNode{Name: "", Path: "", IsDir: true}

	// Walk the union of touched-in-window and seeded paths in a single
	// pass. A `seen` set prevents double-insertion when a path is in
	// both sets.
	//
	// Visibility rule:
	//   - Hot/warm/faint paths: always shown (heat ratio above
	//     HiddenBelow).
	//   - Cold paths: shown only if seeded. A path that was touched
	//     in the window but has decayed below HiddenBelow is dropped
	//     (this preserves the long-standing "tree shows recent
	//     activity" property; the seed is the dedicated mechanism
	//     for surfacing static context, not stale activity).
	//   - Deleted paths: never shown.
	seen := make(map[string]bool, len(t.touches)+len(t.existing))
	insertCandidate := func(path string) {
		if path == "" || seen[path] || t.deleted[path] {
			return
		}
		seen[path] = true
		heat := t.heat[path]
		ratio := heat / maxH
		isCold := ratio < opts.HiddenBelow
		if isCold && !t.existing[path] {
			return
		}
		insertNode(root, path, &TreeNode{
			Name:      basePath(path),
			Path:      path,
			IsDir:     false,
			Touches:   t.touches[path],
			Heat:      heat,
			HeatRatio: ratio,
			Cold:      isCold,
			Faint:     !isCold && ratio < opts.FaintBelow,
			Status:    t.statuses[path],
			NewInThis: t.added[path],
		})
	}
	for path := range t.touches {
		insertCandidate(path)
	}
	for path := range t.existing {
		insertCandidate(path)
	}

	sortTree(root)
	collapseCold(root)
	return root
}

// HeatSnapshot is a renderer-agnostic, JSON-friendly view of the
// per-frame heat map. Used by the HTML output to ship precomputed
// snapshots so the browser doesn't reimplement heat decay.
//
// Deleted paths are filtered out at every entry point: their leftover
// heat/touches entries (kept in TreeState for state continuity) are
// not exposed here. The renderer should treat absence-from-snapshot
// as "this file is gone".
type HeatSnapshot struct {
	// Heat is path -> raw heat (post-decay, pre-normalization).
	Heat map[string]float64
	// Touches is path -> commits that have touched this path so far.
	Touches map[string]int
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
		Added:   make(map[string]bool, len(t.added)),
	}
	for k, v := range t.heat {
		if t.deleted[k] {
			continue
		}
		hs.Heat[k] = v
	}
	for k, v := range t.touches {
		if t.deleted[k] {
			continue
		}
		hs.Touches[k] = v
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
	threshold := opts.HiddenBelow * t.maxHeatFloor()

	hs := HeatSnapshot{
		Heat:    make(map[string]float64),
		Touches: make(map[string]int),
		Added:   make(map[string]bool, len(t.added)),
	}
	for k, v := range t.heat {
		if v < threshold || t.deleted[k] {
			continue
		}
		hs.Heat[k] = v
		hs.Touches[k] = t.touches[k]
	}
	for k, v := range t.added {
		if v {
			hs.Added[k] = true
		}
	}
	return hs
}

// collapseCold walks the tree and folds cold subtrees / cold sibling
// groups into single placeholder rows so the pane stays readable on
// big repos. Returns true if n contains any "hot" descendant (a real
// file that is not Cold).
//
// Rules:
//
//  1. A directory whose entire subtree is cold becomes a single
//     placeholder row (Children dropped, CollapsedCount = total cold
//     files under it). The placeholder keeps the dir's own name so
//     the user can still see "vendor/ … (1247 files)" instead of
//     watching vendor evaporate.
//  2. A directory that hosts at least one hot descendant keeps its
//     hot children (recursed normally) and aggregates its remaining
//     cold *file* siblings into a single trailing placeholder row
//     ("…(N more files)"). Cold *subdirectory* siblings each remain
//     as their own collapsed placeholder so the user sees which
//     neighborhoods are nearby.
//
// The root node is never collapsed; rules apply to its children.
func collapseCold(n *TreeNode) bool {
	if !n.IsDir {
		return !n.Cold
	}

	hotChildren := make([]*TreeNode, 0, len(n.Children))
	coldFileCount := 0
	coldDirPlaceholders := make([]*TreeNode, 0)

	for _, c := range n.Children {
		hot := collapseCold(c)
		if hot {
			hotChildren = append(hotChildren, c)
			continue
		}
		if c.IsDir {
			// Convert a cold subdir into a placeholder row. If the
			// dir has no surviving descendants (because they were
			// filtered out by insertCandidate before collapseCold
			// runs), drop it entirely — a "(0 files)" placeholder
			// would just be noise.
			count := countLeafFiles(c)
			if count == 0 {
				continue
			}
			c.Children = nil
			c.Cold = true
			c.CollapsedCount = count
			coldDirPlaceholders = append(coldDirPlaceholders, c)
		} else {
			// Cold leaf — aggregated into a single sibling row below.
			coldFileCount++
		}
	}

	n.Children = hotChildren
	if coldFileCount > 0 {
		n.Children = append(n.Children, &TreeNode{
			Name:           "…",
			IsDir:          false,
			Cold:           true,
			CollapsedCount: coldFileCount,
		})
	}
	n.Children = append(n.Children, coldDirPlaceholders...)
	return len(hotChildren) > 0
}

// countLeafFiles totals real file leaves under n. Placeholders that
// have already been converted from cold subtrees report their stored
// CollapsedCount instead of their (now-empty) child list — without
// this the count would zero out at the outer recursion level when an
// inner level had already collapsed.
func countLeafFiles(n *TreeNode) int {
	if n.CollapsedCount > 0 {
		return n.CollapsedCount
	}
	if !n.IsDir {
		return 1
	}
	total := 0
	for _, c := range n.Children {
		total += countLeafFiles(c)
	}
	return total
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
