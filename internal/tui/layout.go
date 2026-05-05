// Package tui's layout module owns every constant that controls the
// shape of the screen and the pacing of playback. Everything from
// dwell sizing to render-time card height pulls from this single
// place — drift between the two (e.g. dwell believing 7-row cards
// while rendering uses 11) caused subtle pacing bugs in the past.
package tui

import "time"

// Pane minimum widths. Both must be respected, but the sum cannot
// exceed terminal width — the layout splitter clips them rather than
// stacking floors and overflowing the terminal.
const (
	minTreePaneW   = 28
	minCommitPaneW = 30
)

// Right-pane card layout. expandedCardLines is the height (in rows)
// of one full file card: 1 header row + (expandedCardLines-1) diff
// rows. Increasing this gives each card more scroll capacity but
// fits fewer cards on screen at once.
const (
	expandedCardLines = 11

	// Rows the commit-summary card consumes at the top of the right
	// pane (subject + meta + body? + blank). Used to subtract from
	// the pane budget before deciding how many file cards fit.
	commitCardRows = 5

	// Floor for diff rows per card. We never collapse a card below
	// this — it stops being useful below ~3 lines.
	minDiffRowsPerCard = 3
)

// Approximate chrome-around-right-pane budget for the dwell-sizing
// path. The live View() computes the exact number from rendered
// output, but dwell calc needs a number BEFORE rendering so we
// estimate up front. Off-by-one or two is fine — it only affects
// how many cards we count toward dwell sizing, and the readTail
// gives us a margin of error anyway.
const approxChromeRows = 9

// Pacing knobs that don't fit cleanly inside replay (which is
// renderer-agnostic). frameTickMS is how often Update fires; finer
// gives smoother typing but more redraws.
const frameTickMS = 50 * time.Millisecond

// snapshotInterval controls TreeState caching for fast backward
// navigation. Cache one TreeState clone every N commits so jumping
// back replays at most N commits instead of the full prefix.
const snapshotInterval = 100

// readTail is appended to a commit's typing budget so the user has
// time to read the finished cards before the next commit replaces
// them.
const readTail = 350 * time.Millisecond

// playSpeedSteps is the discrete ladder used by the +/- keys.
// Includes 1.0 so the user can always restore the calibrated
// cadence; arbitrary float arithmetic from a key press would let
// the speed drift off any meaningful point.
var playSpeedSteps = []float64{0.25, 0.5, 0.75, 1.0, 1.5, 2.0, 3.0, 4.0}
