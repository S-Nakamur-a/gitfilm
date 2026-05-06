package tui

import (
	"sort"
	"strings"

	"github.com/S-Nakamur-a/gitfilm/internal/replay"
	"github.com/charmbracelet/lipgloss"
)

// renderLeftPane dispatches between the tree-list and the treemap
// renderers based on viewMode. Centralizes the "what does the left
// pane show" decision so future view modes plug in here.
func (m programModel) renderLeftPane(width, height int) string {
	switch m.viewMode {
	case ViewModeTreemap:
		return m.renderTreemap(width, height)
	default:
		return m.renderTree(width)
	}
}

// renderTreemap fills the left pane with a squarified treemap of the
// currently-live files, weighted by per-file LOC and shaded by
// heat. Each cell is filled with a block character in the heat hue;
// when a cell is large enough (≥ minLabelW columns and ≥ 2 rows),
// the file's basename is overlaid in a contrasting bold style so
// the user can pick out the heaviest hitters at a glance.
//
// The treemap operates in a unit grid: width characters across,
// height rows down. Grid coordinates are rounded to the nearest
// cell, which can leave 1-row gaps between large neighbors — we
// post-process by snapping each rect to fill its cell range,
// trading an ε of weight accuracy for a contiguous picture (treemap
// gaps look like rendering bugs to the eye).
func (m programModel) renderTreemap(width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	items := m.treemapItems()
	if len(items) == 0 {
		return styleDim.Render("(no files yet)")
	}
	rects := replay.TreemapLayout(items, float64(width), float64(height))
	if len(rects) == 0 {
		return styleDim.Render("(no files yet)")
	}

	// Materialize a [height][width] grid of styled cells. Each cell
	// stores its tinted glyph; we emit it row-by-row at the end.
	grid := newCellGrid(width, height)
	maxHeat := m.tree.MaxHeat()
	for _, r := range rects {
		x0, y0 := int(r.X), int(r.Y)
		x1 := int(r.X + r.W)
		y1 := int(r.Y + r.H)
		if x1 > width {
			x1 = width
		}
		if y1 > height {
			y1 = height
		}
		if x1-x0 <= 0 || y1-y0 <= 0 {
			continue
		}
		heat := m.tree.HeatOf(r.Key)
		ratio := 0.0
		if maxHeat > 0 {
			ratio = heat / maxHeat
		}
		fg, bg := treemapCellColors(ratio)
		fillRect(grid, x0, y0, x1, y1, fg, bg)
		if labelW := x1 - x0; labelW >= minLabelW && y1-y0 >= 2 {
			drawLabel(grid, r.Key, x0, y0, labelW, fg, bg)
		}
	}
	return grid.render()
}

// treemapItems builds the layout input from m.tree's per-file LOC.
// Files with zero LOC are skipped (e.g. a fresh add of an empty
// file): they have no area to claim and would just emit empty
// rectangles. Returns nil when nothing is paintable.
//
// The output is sorted by path to break ties deterministically.
// LOCSnapshot's underlying iteration is a Go map (non-deterministic
// order) and TreemapLayout's stable sort only preserves *input*
// order among equal weights — without this sort, ties would shift
// frame-to-frame and the treemap would shimmer. Sorting by path
// freezes that.
func (m programModel) treemapItems() []replay.TreemapItem {
	loc := m.tree.LOCSnapshot()
	if len(loc) == 0 {
		return nil
	}
	out := make([]replay.TreemapItem, 0, len(loc))
	for k, v := range loc {
		if v <= 0 {
			continue
		}
		out = append(out, replay.TreemapItem{Key: k, Weight: float64(v)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// minLabelW is the smallest cell width (in columns) that is worth
// labeling. Below this we either truncate too aggressively or paint
// over the heat color, hurting both legibility and the ratio cue.
const minLabelW = 6

// treemapCellColors picks (foreground, background) for a treemap
// cell based on its heat ratio. We use the same heat tiers as the
// tree-list so users learn one scale across both views; the bg/fg
// pairing is chosen so labels (drawn over the cell in fg) stay
// legible against the same-hue bg.
func treemapCellColors(ratio float64) (lipgloss.Color, lipgloss.Color) {
	switch {
	case ratio < 0.05:
		return lipgloss.Color("245"), lipgloss.Color("236")
	case ratio < 0.25:
		return lipgloss.Color("231"), lipgloss.Color("24")
	case ratio < 0.5:
		return lipgloss.Color("233"), lipgloss.Color("226")
	case ratio < 0.75:
		return lipgloss.Color("232"), lipgloss.Color("214")
	default:
		return lipgloss.Color("231"), lipgloss.Color("196")
	}
}

// cellGrid is a width×height array of (glyph, fg, bg) cells. We
// build the picture cell-by-cell then emit one styled chunk per row
// so we don't emit one ANSI escape per character (lipgloss is happy
// to do that and it's expensive at 80×40 = 3200 cells/frame).
type cellGrid struct {
	w, h  int
	chars [][]rune
	fgs   [][]lipgloss.Color
	bgs   [][]lipgloss.Color
}

func newCellGrid(w, h int) *cellGrid {
	g := &cellGrid{w: w, h: h, chars: make([][]rune, h), fgs: make([][]lipgloss.Color, h), bgs: make([][]lipgloss.Color, h)}
	for y := 0; y < h; y++ {
		g.chars[y] = make([]rune, w)
		g.fgs[y] = make([]lipgloss.Color, w)
		g.bgs[y] = make([]lipgloss.Color, w)
		for x := 0; x < w; x++ {
			g.chars[y][x] = ' '
		}
	}
	return g
}

func fillRect(g *cellGrid, x0, y0, x1, y1 int, fg, bg lipgloss.Color) {
	for y := y0; y < y1 && y < g.h; y++ {
		for x := x0; x < x1 && x < g.w; x++ {
			g.chars[y][x] = ' '
			g.fgs[y][x] = fg
			g.bgs[y][x] = bg
		}
	}
}

// drawLabel writes the file's basename into the top-left of a
// rectangle. Truncates with an ellipsis if it doesn't fit.
func drawLabel(g *cellGrid, path string, x, y, maxW int, fg, bg lipgloss.Color) {
	name := basenameOf(path)
	if len(name) > maxW {
		if maxW < 2 {
			return
		}
		name = name[:maxW-1] + "…"
	}
	for i, r := range name {
		if x+i >= g.w {
			break
		}
		g.chars[y][x+i] = r
		g.fgs[y][x+i] = fg
		g.bgs[y][x+i] = bg
	}
}

// render serializes the grid by streaking runs of identical
// (fg,bg) into a single styled segment. One Render() per run keeps
// the ANSI byte count proportional to "color edges" instead of
// total cells.
func (g *cellGrid) render() string {
	var sb strings.Builder
	for y := 0; y < g.h; y++ {
		x := 0
		for x < g.w {
			start := x
			fg, bg := g.fgs[y][x], g.bgs[y][x]
			for x < g.w && g.fgs[y][x] == fg && g.bgs[y][x] == bg {
				x++
			}
			run := string(g.chars[y][start:x])
			st := lipgloss.NewStyle()
			if fg != "" {
				st = st.Foreground(fg)
			}
			if bg != "" {
				st = st.Background(bg)
			}
			sb.WriteString(st.Render(run))
		}
		if y < g.h-1 {
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}

func basenameOf(path string) string {
	if i := strings.LastIndexByte(path, '/'); i >= 0 {
		return path[i+1:]
	}
	return path
}
