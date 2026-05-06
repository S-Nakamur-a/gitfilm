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
	styleNew      = lipgloss.NewStyle().Foreground(lipgloss.Color("220")).Bold(true)
	styleGhost    = lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Faint(true)
	styleFeat     = lipgloss.NewStyle().Foreground(lipgloss.Color("213"))
	styleAgst     = lipgloss.NewStyle().Foreground(lipgloss.Color("75"))
	stylePane     = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1)

	// Heat-tier styles for the tree pane filename and the footer
	// legend. Same colors are used for both so the user learns the
	// scale once.
	styleHeatCool   = lipgloss.NewStyle().Foreground(lipgloss.Color("39"))
	styleHeatWarm   = lipgloss.NewStyle().Foreground(lipgloss.Color("226"))
	styleHeatHot    = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	styleHeatActive = lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Bold(true)
)

// heatNameStyle picks the style that should color a tree filename
// based on its heat ratio. Same tiers as the footer legend.
func heatNameStyle(ratio float64) lipgloss.Style {
	switch {
	case ratio < 0.25:
		return styleHeatCool
	case ratio < 0.5:
		return styleHeatWarm
	case ratio < 0.75:
		return styleHeatHot
	default:
		return styleHeatActive
	}
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
