package tui

import (
	"testing"
	"time"

	"github.com/S-Nakamur-a/gitfilm/internal/model"
	"github.com/S-Nakamur-a/gitfilm/internal/replay"
)

// TestComputeDwell_DrivenByVisibleFiles guards the user-reported bug:
// previously dwell was sized by the LARGEST file in the commit, even
// when that file was rendered as an offscreen one-line summary. The
// visible cards then finished typing way before dwell ended, leaving
// the screen idle. Now dwell only considers the first `expandable`
// files (which are the ones actually animated as cards).
func TestComputeDwell_DrivenByVisibleFiles(t *testing.T) {
	smallFile := func() model.FileChange {
		return model.FileChange{
			Path:  "a.go",
			Added: 1,
			Hunks: []model.Hunk{{Lines: []model.DiffLine{{Kind: model.LineAdded, Text: "x"}}}},
		}
	}
	hugeFile := func() model.FileChange {
		var lines []model.DiffLine
		for i := 0; i < 200; i++ {
			lines = append(lines, model.DiffLine{Kind: model.LineAdded, Text: "the quick brown fox jumps over the lazy dog"})
		}
		return model.FileChange{
			Path:  "huge.go",
			Added: 200,
			Hunks: []model.Hunk{{Lines: lines}},
		}
	}

	// Tiny terminal => expandable=1 => only first file is visible.
	visibleSmallHiddenHuge := model.History{
		Commits: []model.Commit{{Files: []model.FileChange{smallFile(), hugeFile()}}},
	}
	mSmall := programModel{history: visibleSmallHiddenHuge, idx: 0, height: 14, playSpeed: 1.0}
	if got := mSmall.expandableCount(); got != 1 {
		t.Fatalf("expandableCount = %d, want 1 for height=14", got)
	}
	dSmall := mSmall.computeDwell()

	// Same commit but order swapped: huge is visible, small hidden.
	hiddenSmallVisibleHuge := model.History{
		Commits: []model.Commit{{Files: []model.FileChange{hugeFile(), smallFile()}}},
	}
	mHuge := programModel{history: hiddenSmallVisibleHuge, idx: 0, height: 14, playSpeed: 1.0}
	dHuge := mHuge.computeDwell()

	if !(dSmall < dHuge) {
		t.Errorf("dwell with hidden huge file (%v) should be SHORTER than dwell with visible huge file (%v) — visible-set drives dwell, not hidden files",
			dSmall, dHuge)
	}
	if dSmall > 800*time.Millisecond {
		t.Errorf("dwell on tiny visible file = %v, expected close to MinCommitMS — the offscreen huge file should not extend it", dSmall)
	}
}

// TestEffectiveElapsed_ScaledByPlaySpeed locks in the speed-knob math:
// at 2x, a given dwellElapsed advances effective progress twice as far,
// so the typing rate doubles AND the dwell ends in half the wall time.
func TestEffectiveElapsed_ScaledByPlaySpeed(t *testing.T) {
	m := programModel{dwellElapsed: 1 * time.Second, playSpeed: 2.0}
	if got := m.effectiveElapsed(); got != 2*time.Second {
		t.Errorf("effectiveElapsed at 2x = %v, want 2s", got)
	}
	m.playSpeed = 0.5
	if got := m.effectiveElapsed(); got != 500*time.Millisecond {
		t.Errorf("effectiveElapsed at 0.5x = %v, want 500ms", got)
	}
}

// TestBumpPlaySpeed_StaysOnLadder makes sure +/- moves between the
// calibrated steps and clamps at the ends.
func TestBumpPlaySpeed_StaysOnLadder(t *testing.T) {
	m := programModel{playSpeed: 1.0}
	m.bumpPlaySpeed(+1)
	if m.playSpeed != 1.5 {
		t.Errorf("after +1 from 1.0: got %v, want 1.5", m.playSpeed)
	}
	m.bumpPlaySpeed(+1)
	if m.playSpeed != 2.0 {
		t.Errorf("after +1 from 1.5: got %v, want 2.0", m.playSpeed)
	}
	// clamp at top
	for range 10 {
		m.bumpPlaySpeed(+1)
	}
	if m.playSpeed != playSpeedSteps[len(playSpeedSteps)-1] {
		t.Errorf("not clamped at top: got %v", m.playSpeed)
	}
	// and back down
	for range 20 {
		m.bumpPlaySpeed(-1)
	}
	if m.playSpeed != playSpeedSteps[0] {
		t.Errorf("not clamped at bottom: got %v", m.playSpeed)
	}
}

// TestComputeDwell_UsesReplayClamps still respects the Min/Max bounds
// so trivial commits don't flash by and pathological ones don't grind
// playback to a halt.
func TestComputeDwell_UsesReplayClamps(t *testing.T) {
	// Empty file list => MinFileBudget => well under MinCommitMS.
	m := programModel{
		history:   model.History{Commits: []model.Commit{{Files: nil}}},
		idx:       0,
		height:    30,
		playSpeed: 1.0,
	}
	if got := m.computeDwell(); got < replay.MinCommitMS {
		t.Errorf("dwell %v < MinCommitMS %v", got, replay.MinCommitMS)
	}

	// One enormous file: should saturate at MaxCommitMS.
	var lines []model.DiffLine
	for i := 0; i < 5000; i++ {
		lines = append(lines, model.DiffLine{Kind: model.LineAdded, Text: "the quick brown fox jumps over the lazy dog"})
	}
	huge := model.FileChange{
		Path:  "huge.go",
		Added: 5000,
		Hunks: []model.Hunk{{Lines: lines}},
	}
	m2 := programModel{
		history:   model.History{Commits: []model.Commit{{Files: []model.FileChange{huge}}}},
		idx:       0,
		height:    30,
		playSpeed: 1.0,
	}
	if got := m2.computeDwell(); got > replay.MaxCommitMS {
		t.Errorf("dwell %v > MaxCommitMS %v", got, replay.MaxCommitMS)
	}
}
