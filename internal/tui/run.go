package tui

import "github.com/S-Nakamur-a/gitplay/internal/model"

// Run is the TUI entrypoint. The real Bubble Tea program is wired in
// the next task — for now we just print a summary so the rest of the
// CLI compiles and `--output html` is fully usable.
func Run(h model.History) error {
	return runProgram(h)
}
