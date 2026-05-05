package model

import "math"

// ChurnState tracks per-file "heat" with exponential decay across frames.
// Heat is bumped by (added + removed) on each commit that touches a file,
// and decayed by a configurable half-life measured in commits.
//
// Use:
//
//	state := NewChurnState(7)
//	for _, c := range history.Commits {
//	    state.Step(c)
//	    heat := state.Heat("src/parser/lexer.go")
//	}
type ChurnState struct {
	halfLife float64
	decay    float64 // multiplier applied each Step BEFORE adding new churn
	heat     map[string]float64
	touches  map[string]int
}

// NewChurnState creates a churn tracker with the given half-life in commits.
// halfLife <= 0 disables decay (heat is purely cumulative).
func NewChurnState(halfLife float64) *ChurnState {
	decay := 1.0
	if halfLife > 0 {
		decay = math.Pow(0.5, 1.0/halfLife)
	}
	return &ChurnState{
		halfLife: halfLife,
		decay:    decay,
		heat:     make(map[string]float64),
		touches:  make(map[string]int),
	}
}

// Step advances the state by one commit, decaying all existing heat and
// adding fresh churn for files touched in c. Renames carry heat from the
// old path to the new path (heat is preserved, not duplicated).
func (s *ChurnState) Step(c Commit) {
	if s.halfLife > 0 {
		for k := range s.heat {
			s.heat[k] *= s.decay
		}
	}
	for _, f := range c.Files {
		if f.Status == StatusRenamed && f.OldPath != "" && f.OldPath != f.Path {
			s.heat[f.Path] += s.heat[f.OldPath]
			s.touches[f.Path] += s.touches[f.OldPath]
			delete(s.heat, f.OldPath)
			delete(s.touches, f.OldPath)
		}
		s.heat[f.Path] += float64(f.Added + f.Removed)
		s.touches[f.Path]++
		if f.Status == StatusDeleted {
			// keep heat around briefly so the UI can show a fading ghost,
			// but mark touches as a deletion event by leaving heat as-is.
		}
	}
}

// Heat returns the current heat for a path (0 if untracked).
func (s *ChurnState) Heat(path string) float64 {
	return s.heat[path]
}

// Touches returns how many commits have touched the path so far.
func (s *ChurnState) Touches(path string) int {
	return s.touches[path]
}

// MaxHeat returns the largest heat value currently tracked (useful for
// normalizing colors). Returns 0 if there is no heat.
func (s *ChurnState) MaxHeat() float64 {
	max := 0.0
	for _, v := range s.heat {
		if v > max {
			max = v
		}
	}
	return max
}

// Snapshot returns a copy of the current heat map. Intended for tests
// and for HTML export.
func (s *ChurnState) Snapshot() map[string]float64 {
	out := make(map[string]float64, len(s.heat))
	for k, v := range s.heat {
		out[k] = v
	}
	return out
}
