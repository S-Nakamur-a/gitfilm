package tui

import (
	"github.com/S-Nakamur-a/gitfilm/internal/model"
	"github.com/S-Nakamur-a/gitfilm/internal/replay"
	"github.com/charmbracelet/lipgloss"
)

// Module-level styles. Keep these as vars (not consts of method
// chains) so callers compose without re-allocating the underlying
// style each render.
var (
	styleTitle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212"))
	styleSubject  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("231"))
	styleFilePath = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("117"))
	styleDim      = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	styleAdd      = lipgloss.NewStyle().Foreground(lipgloss.Color("46"))
	styleDel      = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	styleNew = lipgloss.NewStyle().Foreground(lipgloss.Color("220")).Bold(true)
	// styleFaintName paints tree-list filenames whose heat is in
	// [HiddenBelow, FaintBelow) — alive but cooled. Drawn dim so the
	// row reads as "still here, not the focus".
	styleFaintName = lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Faint(true)
	// styleCold is used for "exists but not active" rows: the cold
	// tier of real files in the seeded set, plus the …(N files)
	// placeholders that summarize cold subtrees and sibling groups.
	// Deliberately one shade darker than styleDim so the eye learns
	// "cold = repo context, dim = inline annotation".
	styleCold = lipgloss.NewStyle().Foreground(lipgloss.Color("238")).Faint(true)
	styleFeat = lipgloss.NewStyle().Foreground(lipgloss.Color("213"))
	styleAgst = lipgloss.NewStyle().Foreground(lipgloss.Color("75"))
	stylePane = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1)
)

// heatTier is one row of the heat-color scale. The same tier drives
// the tree filename color, the treemap cell colors, and the footer
// legend, so the user learns one scale that holds across views.
//
// Why a single-hue ramp (amber → bright orange) instead of four
// unrelated hues: distinct hues read as four *categories* (blue is
// not "less than" yellow). A monochrome ramp reads as one continuous
// intensity, which matches what `HeatRatio` actually means.
type heatTier struct {
	Name      string         // legend label
	TreeFG    lipgloss.Color // filename color in the tree pane
	TreemapBG lipgloss.Color // cell background in the treemap
	TreemapFG lipgloss.Color // label color drawn over the bg
	Bold      bool           // bold the tree name (used for the top tier)
}

// heatPalette is the *only* place heat colors are defined. Edit this
// table to retheme; downstream call sites all derive from it.
//
// Tier boundaries are quartiles of HeatRatio:
//
//	[0.00, 0.25) cool    [0.25, 0.50) warm
//	[0.50, 0.75) hot     [0.75, 1.00] active
//
// The ramp is xterm 94 → 130 → 166 → 208 (#875f00 → #ff8700), an
// amber-to-orange progression. We avoid the red end of the spectrum
// because the right pane already uses red for `del` lines.
var heatPalette = [4]heatTier{
	{Name: "cool", TreeFG: lipgloss.Color("94"), TreemapBG: lipgloss.Color("94"), TreemapFG: lipgloss.Color("231")},
	{Name: "warm", TreeFG: lipgloss.Color("130"), TreemapBG: lipgloss.Color("130"), TreemapFG: lipgloss.Color("231")},
	{Name: "hot", TreeFG: lipgloss.Color("166"), TreemapBG: lipgloss.Color("166"), TreemapFG: lipgloss.Color("232")},
	{Name: "active", TreeFG: lipgloss.Color("208"), TreemapBG: lipgloss.Color("208"), TreemapFG: lipgloss.Color("232"), Bold: true},
}

// heatTierIndex maps a HeatRatio in [0,1] onto the heatPalette index.
func heatTierIndex(ratio float64) int {
	switch {
	case ratio < 0.25:
		return 0
	case ratio < 0.5:
		return 1
	case ratio < 0.75:
		return 2
	default:
		return 3
	}
}

// heatNameStyle picks the style that should color a tree filename
// based on its heat ratio. Same tiers as the footer legend.
func heatNameStyle(ratio float64) lipgloss.Style {
	t := heatPalette[heatTierIndex(ratio)]
	s := lipgloss.NewStyle().Foreground(t.TreeFG)
	if t.Bold {
		s = s.Bold(true)
	}
	return s
}

// tagLabel renders the bracketed branch-tag chip used in headers.
func tagLabel(t model.BranchTag) string {
	switch t {
	case model.BranchTagFeature:
		return styleFeat.Render("[feat]")
	case model.BranchTagAgainst:
		return styleAgst.Render("[main]")
	default:
		return styleDim.Render("[?]")
	}
}

// authorChip renders a colored author-name chip used in headers and
// commit cards. Each author maps to a stable palette color via
// replay.AuthorColor — across the run, the same name always gets the
// same hue, so the eye learns "who's on screen" without reading.
//
// Bold is intentional: the chip needs to compete visually with the
// branch tag and date that sit next to it on the metadata line.
func authorChip(name string) string {
	if name == "" {
		return ""
	}
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color(replay.AuthorColor(name))).
		Bold(true).
		Render(name)
}

// statusBadge renders the one-letter A/M/D/R/C indicator next to a
// file path in the right pane.
func statusBadge(s model.ChangeStatus) string {
	switch s {
	case model.StatusAdded:
		return styleAdd.Render("A")
	case model.StatusDeleted:
		return styleDel.Render("D")
	case model.StatusRenamed:
		return styleNew.Render("R")
	case model.StatusCopied:
		return styleNew.Render("C")
	default:
		return styleDim.Render("M")
	}
}
