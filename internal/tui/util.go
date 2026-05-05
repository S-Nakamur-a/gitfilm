package tui

import (
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

// truncate caps the rune-length of s at max, using "…" as the
// elision marker when content is dropped. Operates on runes so
// multibyte characters aren't cut mid-codepoint.
func truncate(s string, max int) string {
	if max <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	if max < 1 {
		return ""
	}
	return string(r[:max-1]) + "…"
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
