package tui

import (
	"fmt"
	"strings"

	"github.com/S-Nakamur-a/gitfilm/internal/model"
	"github.com/S-Nakamur-a/gitfilm/internal/replay"
)

// renderRight paints the commit-summary card at the top of the
// right pane plus the per-file diff cards below it. Layout is
// driven entirely by `height`: the larger the pane, the more file
// cards we expand, with the leftover collapsing to one-line
// summaries.
func (m programModel) renderRight(c model.Commit, width, height int) string {
	var sb strings.Builder
	writeCommitCard(&sb, c, width)

	if len(c.Files) == 0 {
		sb.WriteString(styleDim.Render("(no file changes in this commit)"))
		return sb.String()
	}

	expandable, maxDiffLines := cardSlots(height, len(c.Files))
	units := int(m.effectiveElapsed().Seconds() * replay.UnitsPerSecond)
	sep := styleDim.Render(strings.Repeat("─", width)) + "\n"

	for i, f := range c.Files {
		anim := replay.ApplyFile(f, capUnits(units, replay.FileBudget(f)))
		if i > 0 {
			sb.WriteString(sep)
		}
		if i < expandable {
			sb.WriteString(renderFileCard(f, anim, width, maxDiffLines))
		} else {
			sb.WriteString(renderFileLine(f, anim, width))
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}

// writeCommitCard appends the subject + meta + body lines that
// open the right pane. Uses the same height budget allocation
// (commitCardRows) as cardSlots assumes.
func writeCommitCard(sb *strings.Builder, c model.Commit, width int) {
	sb.WriteString(styleSubject.Render(truncate(c.Subject, width)))
	sb.WriteByte('\n')
	sb.WriteString(styleDim.Render(fmt.Sprintf("%s · %s · %s · %s",
		c.AuthorName,
		c.When.Format("2006-01-02 15:04"),
		tagLabel(c.Tag),
		c.ShortHash)))
	sb.WriteByte('\n')
	if body := firstNonEmptyLine(c.Body); body != "" {
		sb.WriteString(styleDim.Render(truncate(body, width)))
		sb.WriteByte('\n')
	}
	sb.WriteByte('\n')
}

// cardSlots decides how many file cards expand to full diff cards
// and how many diff rows each gets, given the pane's available
// height. Files beyond `expandable` collapse to one-line summaries.
func cardSlots(paneHeight, fileCount int) (expandable, maxDiffLines int) {
	available := paneHeight - commitCardRows
	if available < 4 {
		available = 4
	}
	expandable = available / expandedCardLines
	if expandable < 1 {
		expandable = 1
	}
	if expandable > fileCount {
		expandable = fileCount
	}
	maxDiffLines = available/expandable - 1
	if maxDiffLines < minDiffRowsPerCard {
		maxDiffLines = minDiffRowsPerCard
	}
	return expandable, maxDiffLines
}

// capUnits clips the global typing units so a per-file animation
// doesn't request more units than the file actually carries.
func capUnits(globalUnits, fileBudget int) int {
	if globalUnits > fileBudget {
		return fileBudget
	}
	return globalUnits
}

// renderFileLine is the one-line summary used for files beyond
// the expandable budget. Same animation cursor is used for the
// ✓/▸ indicator so partial-typed files still read as in-flight.
func renderFileLine(f model.FileChange, a replay.FileAnim, width int) string {
	mark := "  "
	if a.Done {
		mark = styleAdd.Render("✓ ")
	} else if a.HunkIdx > 0 || a.LineIdx > 0 || a.CharsInLine > 0 {
		mark = styleNew.Render("▸ ")
	}
	stats := styleAdd.Render(fmt.Sprintf("+%d", f.Added)) + styleDel.Render(fmt.Sprintf(" -%d", f.Removed))
	return fmt.Sprintf("%s%s %s  %s",
		mark,
		statusBadge(f.Status),
		styleFilePath.Render(truncate(f.Path, width-12)),
		stats)
}

// renderFileCard is the multi-row card with header + scroll-tail
// diff window. The window anchors the typing cursor at the bottom,
// so the user always sees the line being typed plus
// preceding-context. When done with a hunk, tails the end.
func renderFileCard(f model.FileChange, a replay.FileAnim, width int, maxDiffLines int) string {
	var sb strings.Builder
	sb.WriteString(fileCardHeader(f, a))
	sb.WriteByte('\n')

	if len(f.Hunks) == 0 || maxDiffLines < 1 {
		return sb.String()
	}

	hi := a.HunkIdx
	if hi >= len(f.Hunks) {
		hi = len(f.Hunks) - 1
	}
	h := f.Hunks[hi]

	start, end, hidden := cardLineWindow(a.LineIdx, len(h.Lines), maxDiffLines, a.Done)
	if hidden > 0 {
		sb.WriteString(styleDim.Render(fmt.Sprintf("    … %d earlier line%s", hidden, pluralS(hidden))))
		sb.WriteByte('\n')
	}
	for li := start; li < end; li++ {
		writeDiffLine(&sb, h.Lines[li], a, li, width)
	}
	return sb.String()
}

func fileCardHeader(f model.FileChange, a replay.FileAnim) string {
	mark := "▸"
	markStyle := styleNew
	if a.Done {
		mark = "✓"
		markStyle = styleAdd
	}
	stats := styleAdd.Render(fmt.Sprintf("+%d", f.Added)) + styleDel.Render(fmt.Sprintf(" -%d", f.Removed))
	header := markStyle.Render(mark) + " " +
		statusBadge(f.Status) + " " +
		styleFilePath.Render(f.Path) + "  " + stats
	if len(f.Hunks) > 1 {
		hi := a.HunkIdx
		if hi >= len(f.Hunks) {
			hi = len(f.Hunks) - 1
		}
		header += "  " + styleDim.Render(fmt.Sprintf("hunk %d/%d", hi+1, len(f.Hunks)))
	}
	return header
}

func writeDiffLine(sb *strings.Builder, l model.DiffLine, a replay.FileAnim, li, width int) {
	text := l.Text
	showCaret := false
	if !a.Done && li == a.LineIdx && l.Kind == model.LineAdded {
		text = replay.PartialLine(l.Text, a.CharsInLine)
		showCaret = true
	}
	switch l.Kind {
	case model.LineAdded:
		line := "  + " + truncate(text, width-5)
		if showCaret {
			line += "▌"
		}
		sb.WriteString(styleAdd.Render(line))
	case model.LineRemoved:
		sb.WriteString(styleDel.Render("  - " + truncate(l.Text, width-5)))
	default:
		sb.WriteString(styleDim.Render("    " + truncate(l.Text, width-5)))
	}
	sb.WriteByte('\n')
}

// cardLineWindow picks the visible [start, end) slice of the
// active hunk's lines so the typing cursor stays anchored at the
// bottom of the window — i.e. tail-follow scrolling. When done,
// tails the end. The hidden return is the number of lines
// suppressed above the window (used to print a "… N earlier"
// indicator and reserve a row for it).
//
// Total visible rows equals visibleRows (assuming totalLines >=
// visibleRows): one row goes to the indicator when scrolled, the
// rest to actual diff lines.
func cardLineWindow(cursor, totalLines, visibleRows int, done bool) (start, end, hidden int) {
	if totalLines <= 0 || visibleRows < 1 {
		return 0, 0, 0
	}
	end = totalLines
	if !done {
		end = cursor + 1
		if end > totalLines {
			end = totalLines
		}
		if end < 1 {
			end = 1
		}
	}
	start = end - visibleRows
	if start <= 0 {
		return 0, end, 0
	}
	start += 1 // reserve a row for the "… N earlier" indicator
	if start > end {
		start = end
	}
	hidden = start
	return start, end, hidden
}
