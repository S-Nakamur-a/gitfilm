package tui

import (
	"fmt"
	"strings"

	"github.com/S-Nakamur-a/gitfilm/internal/replay"
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
		sb.WriteString(truncate(treeRow(n, prefix), width))
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
	case n.Deleted:
		return prefix + styleGhost.Render("👻 "+name+" (deleted)")
	case n.IsDir:
		return prefix + name
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
