package tui

import (
	"time"

	"github.com/S-Nakamur-a/gitfilm/internal/replay"
)

// effectiveElapsed scales raw dwellElapsed by playSpeed. Used by
// both the auto-advance check and the typing units rate so they
// stay in lockstep — a 2x speed advances both proportionally.
func (m programModel) effectiveElapsed() time.Duration {
	return time.Duration(float64(m.dwellElapsed) * m.playSpeed)
}

// commitProgress returns the user-visible fraction (0..1) through
// the current commit's dwell, clamped. Drives the per-commit
// progress bar and the timeline caret.
func (m programModel) commitProgress() float64 {
	if m.commitDwell <= 0 {
		return 0
	}
	f := float64(m.effectiveElapsed()) / float64(m.commitDwell)
	if f < 0 {
		f = 0
	}
	if f > 1 {
		f = 1
	}
	return f
}

// expandableCount returns how many full file cards fit in the
// right pane at the current terminal height. Both the dwell-sizing
// path and the rendering path call this so they agree on which
// files contribute to dwell.
//
// When height isn't known yet (pre-WindowSizeMsg), assumes a
// reasonable default so the very first frame doesn't show 0
// expandable cards.
func (m programModel) expandableCount() int {
	h := m.height
	if h <= 0 {
		h = 30
	}
	bodyH := h - approxChromeRows
	if bodyH < 4 {
		bodyH = 4
	}
	available := bodyH - commitCardRows
	if available < 4 {
		available = 4
	}
	expandable := available / expandedCardLines
	if expandable < 1 {
		expandable = 1
	}
	return expandable
}

// computeDwell sizes the dwell to the largest *visible* file's
// budget, plus a read tail. Sizing by the offscreen-largest file
// (replay.CommitMaxBudget) made commits with hidden huge files
// idle for seconds after the visible cards finished typing.
func (m programModel) computeDwell() time.Duration {
	if m.idx < 0 || m.idx >= len(m.history.Commits) {
		return replay.MinCommitMS
	}
	c := m.history.Commits[m.idx]
	expandable := m.expandableCount()
	maxB := 0
	for i, f := range c.Files {
		if i >= expandable {
			break
		}
		if b := replay.FileBudget(f); b > maxB {
			maxB = b
		}
	}
	if maxB == 0 {
		maxB = replay.MinFileBudget
	}
	secs := float64(maxB) / replay.UnitsPerSecond
	d := time.Duration(secs*float64(time.Second)) + readTail
	if d < replay.MinCommitMS {
		d = replay.MinCommitMS
	}
	if d > replay.MaxCommitMS {
		d = replay.MaxCommitMS
	}
	return d
}

// bumpPlaySpeed steps along playSpeedSteps. Snaps to the nearest
// step first when the current speed isn't on the ladder
// (defensive — could only happen via persisted state someday).
func (m *programModel) bumpPlaySpeed(dir int) {
	cur := nearestSpeedIdx(m.playSpeed)
	next := cur + dir
	if next < 0 {
		next = 0
	}
	if next >= len(playSpeedSteps) {
		next = len(playSpeedSteps) - 1
	}
	m.playSpeed = playSpeedSteps[next]
}

func nearestSpeedIdx(speed float64) int {
	cur := 0
	bestDiff := 1e9
	for i, s := range playSpeedSteps {
		d := s - speed
		if d < 0 {
			d = -d
		}
		if d < bestDiff {
			bestDiff = d
			cur = i
		}
	}
	return cur
}
