package tui

import (
	"github.com/S-Nakamur-a/gitfilm/internal/replay"
)

// advance moves idx by n (clamped to history bounds) and replays
// tree state.
func (m *programModel) advance(n int) {
	target := m.idx + n
	if target < 0 {
		target = 0
	}
	if target >= len(m.history.Commits) {
		target = len(m.history.Commits) - 1
	}
	m.jumpTo(target)
}

// jumpTo seeks to `target`. Backwards jumps use the periodic
// snapshot cache so we never replay more than snapshotInterval
// commits. Forward jumps just step from the current position.
func (m *programModel) jumpTo(target int) {
	if target == m.idx {
		return
	}
	if target < m.idx {
		baseIdx, base := m.nearestSnapshot(target)
		st := base.Clone()
		for i := baseIdx + 1; i <= target; i++ {
			st.Step(m.history.Commits[i])
		}
		m.tree = st
	} else {
		for i := m.idx + 1; i <= target; i++ {
			m.tree.Step(m.history.Commits[i])
			m.maybeSnapshot(i)
		}
	}
	m.idx = target
	m.dwellElapsed = 0
	if target >= 0 && target < len(m.history.Commits) {
		m.commitDwell = m.computeDwell()
	}
}

// nearestSnapshot returns the snapshot whose stepping range ends
// closest at or before target. Falls back to a "before commit 0"
// state (with baseIdx = -1) when no snapshot is suitable. When a
// seedBase is present that fallback Clone preserves the seeded
// existing-set so backward jumps to the very start still render
// the surrounding repo context.
func (m *programModel) nearestSnapshot(target int) (int, *replay.TreeState) {
	bucket := target / snapshotInterval
	if bucket < len(m.snapshots) && m.snapshots[bucket] != nil {
		return bucket*snapshotInterval + (snapshotInterval - 1), m.snapshots[bucket]
	}
	for b := bucket - 1; b >= 0; b-- {
		if b < len(m.snapshots) && m.snapshots[b] != nil {
			return b*snapshotInterval + (snapshotInterval - 1), m.snapshots[b]
		}
	}
	if m.seedBase != nil {
		return -1, m.seedBase
	}
	return -1, replay.NewTreeState(replay.DefaultHalfLife)
}

// maybeSnapshot records a TreeState clone after stepping through
// commit i, but only on bucket boundaries.
func (m *programModel) maybeSnapshot(i int) {
	if (i+1)%snapshotInterval != 0 {
		return
	}
	m.recordSnapshot(i, m.tree)
}

// recordSnapshot stores a clone of `src` at the bucket that
// corresponds to absolute commit index `i`. No-op if a snapshot
// for that bucket is already present.
func (m *programModel) recordSnapshot(i int, src *replay.TreeState) {
	bucket := i / snapshotInterval
	for len(m.snapshots) <= bucket {
		m.snapshots = append(m.snapshots, nil)
	}
	if m.snapshots[bucket] == nil {
		m.snapshots[bucket] = src.Clone()
	}
}
