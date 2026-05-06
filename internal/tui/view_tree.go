package tui

import (
	"fmt"
	"strings"

	"github.com/S-Nakamur-a/gitfilm/internal/replay"
	"github.com/charmbracelet/x/ansi"
)

// renderTree paints the left pane: a filesystem tree where each
// filename's color encodes its current heat tier. The bar approach
// (a 6-cell colored gauge) was dropped because the bar was hard to
// notice and added nested ANSI that bled into the right pane.
func (m programModel) renderTree(width int) string {
	root := m.tree.Snapshot()
	var sb strings.Builder
	renderNode(&sb, root, "", true, width)
	return sb.String()
}

func renderNode(sb *strings.Builder, n *replay.TreeNode, prefix string, isRoot bool, width int) {
	if !isRoot {
		// treeRow returns a string with embedded ANSI escapes from
		// heat / status styles; rune-based truncation can cut mid-CSI
		// and let a broken escape eat the right pane's border. Use
		// ansi.Truncate so the cut respects escape boundaries and
		// counts visible width, not bytes.
		sb.WriteString(ansi.Truncate(treeRow(n, prefix), width, "…"))
		sb.WriteByte('\n')
	}
	for i, c := range n.Children {
		var branch string
		if !isRoot {
			if i == len(n.Children)-1 {
				branch = "└ "
			} else {
				branch = "├ "
			}
		}
		renderNode(sb, c, prefix+branch, false, width)
	}
}

// treeRow assembles one tree row from non-overlapping styled
// segments. Never feed a pre-rendered (already-ANSI) string back
// through another .Render() — the inner SGR-reset closes the
// outer style and leaves the rest of the row "stuck".
func treeRow(n *replay.TreeNode, prefix string) string {
	name := n.Name
	if n.IsDir {
		name = name + "/"
	}
	switch {
	case n.CollapsedCount > 0 && n.IsDir:
		// Cold subtree placeholder: "<dirname>/  …(N files)".
		return prefix + styleCold.Render(fmt.Sprintf("%s  …(%d %s)", name, n.CollapsedCount, pluralFiles(n.CollapsedCount)))
	case n.CollapsedCount > 0:
		// Sibling-leaves placeholder: "…(N more files)".
		return prefix + styleCold.Render(fmt.Sprintf("…(%d more %s)", n.CollapsedCount, pluralFiles(n.CollapsedCount)))
	case n.Deleted:
		return prefix + styleGhost.Render("👻 "+name+" (deleted)")
	case n.IsDir:
		return prefix + name
	case n.Cold:
		// Real cold leaf — kept visible because it was seeded but
		// never made it into a placeholder aggregation (e.g. it is
		// the only child of an otherwise-hot dir). Render with no
		// marker, no heat color.
		return prefix + styleCold.Render(name)
	case n.Faint:
		return prefix + treeMarker(n) + styleGhost.Render(name)
	default:
		touches := ""
		if n.Touches > 0 {
			touches = styleDim.Render(fmt.Sprintf("  ×%d", n.Touches))
		}
		return prefix + treeMarker(n) + heatNameStyle(n.HeatRatio).Render(name) + touches
	}
}

// pluralFiles returns "file" or "files" depending on n. The existing
// pluralS helper in util.go appends a suffix to one word, but here we
// want the noun itself; expanding inline keeps the format string
// readable without complicating pluralS's contract.
func pluralFiles(n int) string {
	if n == 1 {
		return "file"
	}
	return "files"
}

func treeMarker(n *replay.TreeNode) string {
	switch {
	case n.Deleted:
		return styleGhost.Render("👻 ")
	case n.NewInThis:
		return styleNew.Render("✨ ")
	default:
		return ""
	}
}
