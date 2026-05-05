package model

import (
	"math"
	"testing"
)

func mkCommit(files ...FileChange) Commit {
	return Commit{Files: files}
}

func TestChurn_NoDecayCumulative(t *testing.T) {
	s := NewChurnState(0) // disabled
	s.Step(mkCommit(FileChange{Path: "a.go", Added: 10, Removed: 2}))
	s.Step(mkCommit(FileChange{Path: "a.go", Added: 5, Removed: 0}))
	got := s.Heat("a.go")
	want := 17.0
	if got != want {
		t.Fatalf("heat = %v, want %v", got, want)
	}
	if s.Touches("a.go") != 2 {
		t.Fatalf("touches = %d, want 2", s.Touches("a.go"))
	}
}

func TestChurn_HalfLifeApproxHalvesAtN(t *testing.T) {
	// With half-life = 4, after 4 idle steps the heat should be ~half.
	s := NewChurnState(4)
	s.Step(mkCommit(FileChange{Path: "a.go", Added: 100}))
	initial := s.Heat("a.go")
	for i := 0; i < 4; i++ {
		s.Step(mkCommit()) // idle
	}
	got := s.Heat("a.go")
	if math.Abs(got-initial/2) > 0.0001 {
		t.Fatalf("heat after 4 idle steps = %v, want ~%v", got, initial/2)
	}
}

func TestChurn_DecayAppliesBeforeNewChurn(t *testing.T) {
	// With half-life = 1, the previous heat halves before today's churn lands.
	s := NewChurnState(1)
	s.Step(mkCommit(FileChange{Path: "a.go", Added: 10}))
	s.Step(mkCommit(FileChange{Path: "a.go", Added: 6}))
	// step 1: 0 -> 10
	// step 2: 10 * 0.5 = 5; +6 -> 11
	if got := s.Heat("a.go"); math.Abs(got-11) > 0.0001 {
		t.Fatalf("heat = %v, want 11", got)
	}
}

func TestChurn_RenameCarriesHeat(t *testing.T) {
	s := NewChurnState(0)
	s.Step(mkCommit(FileChange{Path: "old.go", Added: 50}))
	s.Step(mkCommit(FileChange{
		Path:    "new.go",
		OldPath: "old.go",
		Status:  StatusRenamed,
		Added:   3,
	}))
	if got := s.Heat("new.go"); got != 53 {
		t.Fatalf("new.go heat = %v, want 53", got)
	}
	if got := s.Heat("old.go"); got != 0 {
		t.Fatalf("old.go heat = %v, want 0 after rename", got)
	}
	if got := s.Touches("new.go"); got != 2 {
		t.Fatalf("new.go touches = %d, want 2", got)
	}
}

func TestChurn_MaxHeat(t *testing.T) {
	s := NewChurnState(0)
	s.Step(mkCommit(
		FileChange{Path: "a.go", Added: 5},
		FileChange{Path: "b.go", Added: 30},
		FileChange{Path: "c.go", Added: 12},
	))
	if got := s.MaxHeat(); got != 30 {
		t.Fatalf("max heat = %v, want 30", got)
	}
}
