package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// clipPane bounds rendered content to a pane's inner box: at most
// `height` lines, each at most `width` cells wide. Truncation is
// ANSI-aware so colored output doesn't spill into the neighboring
// pane.
func clipPane(s string, width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	lines := strings.Split(s, "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	for i, l := range lines {
		if ansi.StringWidth(l) > width {
			lines[i] = ansi.Truncate(l, width, "")
		}
	}
	return strings.Join(lines, "\n")
}

// truncate caps the *visible cell width* of s at max, using "…" as
// the elision marker when content is dropped. Delegates to
// ansi.Truncate so the cut respects ANSI escape boundaries (no
// broken CSI bleeding into neighboring panes) and counts wide
// characters (CJK / emoji) as 2 cells. Plain ASCII callers get the
// same behavior as the previous rune-count implementation.
func truncate(s string, max int) string {
	if max <= 0 {
		return ""
	}
	return ansi.Truncate(s, max, "…")
}

// firstNonEmptyLine returns the first non-blank line of s, trimmed.
// Used to surface the body's first sentence in a one-row context.
func firstNonEmptyLine(s string) string {
	for _, ln := range strings.Split(s, "\n") {
		ln = strings.TrimSpace(ln)
		if ln != "" {
			return ln
		}
	}
	return ""
}

// pluralS returns "" for 1 and "s" otherwise — used inline in
// fmt.Sprintf("%d line%s", n, pluralS(n)).
func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// scrollWindow returns the [offset, offset+height) slice of s split
// on '\n', clamped so the window never extends past the last line,
// with `↑ N more` / `↓ N more` hint rows replacing the top/bottom
// row whenever content is hidden in that direction. The hint costs
// one row of visible content per overflow side — we treat that as
// part of paying for the scrolling affordance, which is more
// honest than silently cutting content (the prior behavior).
//
// Returns the input unchanged when total lines fit inside height
// (no scrolling needed) or height <= 0.
//
// Clamping is internal-only: the offset stored on the model is not
// updated. Bubble Tea's View runs on a value-receiver copy, so
// write-back would be lost anyway. The cost is "rapid bashing past
// the bottom then pressing ↑ once" requires a few extra presses
// before the window starts moving. Callers can mitigate by capping
// offset growth at the keyboard handler with an over-estimate (see
// scrollGrowCap usage in update.go) instead of letting it diverge.
func scrollWindow(s string, offset, height int) string {
	if height <= 0 {
		return ""
	}
	lines := strings.Split(s, "\n")
	total := len(lines)
	if total <= height {
		return s
	}
	maxOff := total - height
	if offset > maxOff {
		offset = maxOff
	}
	if offset < 0 {
		offset = 0
	}
	visible := make([]string, height)
	copy(visible, lines[offset:offset+height])
	if offset > 0 {
		visible[0] = styleDim.Render(fmt.Sprintf("↑ %d more above", offset))
	}
	if offset+height < total {
		hidden := total - (offset + height)
		visible[height-1] = styleDim.Render(fmt.Sprintf("↓ %d more below", hidden))
	}
	return strings.Join(visible, "\n")
}
