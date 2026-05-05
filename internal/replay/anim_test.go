package replay

import (
	"testing"

	"github.com/S-Nakamur-a/gitfilm/internal/model"
)

func mkHunk(kinds ...struct {
	k model.DiffLineKind
	t string
}) model.Hunk {
	h := model.Hunk{}
	for _, x := range kinds {
		h.Lines = append(h.Lines, model.DiffLine{Kind: x.k, Text: x.t})
	}
	return h
}

func added(s string) struct {
	k model.DiffLineKind
	t string
} {
	return struct {
		k model.DiffLineKind
		t string
	}{model.LineAdded, s}
}
func ctx(s string) struct {
	k model.DiffLineKind
	t string
} {
	return struct {
		k model.DiffLineKind
		t string
	}{model.LineContext, s}
}

func mkFC(hunks ...model.Hunk) model.FileChange {
	return model.FileChange{Path: "x.go", Hunks: hunks}
}

func TestApplyFile_BudgetZeroIsStartCursor(t *testing.T) {
	f := mkFC(mkHunk(added("hello")))
	a := ApplyFile(f, 0)
	if a != (FileAnim{}) {
		t.Fatalf("budget 0 = %+v, want zero value", a)
	}
}

func TestApplyFile_TypingWithinAddedLine(t *testing.T) {
	f := mkFC(mkHunk(added("hello world")))
	a := ApplyFile(f, 5)
	if a.LineIdx != 0 || a.CharsInLine != 5 || a.Done {
		t.Fatalf("got %+v, want LineIdx=0 CharsInLine=5", a)
	}
}

func TestApplyFile_AdvancesPastFullLine(t *testing.T) {
	f := mkFC(mkHunk(added("ab"), added("cd")))
	a := ApplyFile(f, 2)
	if a.LineIdx != 1 || a.CharsInLine != 0 || a.Done {
		t.Fatalf("got %+v after 2 units", a)
	}
	a = ApplyFile(f, 3)
	if a.LineIdx != 1 || a.CharsInLine != 1 {
		t.Fatalf("got %+v after 3 units", a)
	}
}

func TestApplyFile_ContextLineCostsLineCost(t *testing.T) {
	f := mkFC(mkHunk(ctx("ctx"), added("ab")))
	a := ApplyFile(f, LineCost)
	if a.LineIdx != 1 || a.CharsInLine != 0 {
		t.Fatalf("got %+v after LineCost", a)
	}
	a = ApplyFile(f, 2)
	if a.LineIdx != 0 || a.CharsInLine != 0 {
		t.Fatalf("got %+v with insufficient budget", a)
	}
}

func TestApplyFile_DoneOnceConsumed(t *testing.T) {
	f := mkFC(mkHunk(added("ab")))
	a := ApplyFile(f, 1000)
	if !a.Done || a.CharsInLine != -1 {
		t.Fatalf("expected Done with CharsInLine=-1, got %+v", a)
	}
}

func TestApplyFile_HunkGapCrossing(t *testing.T) {
	f := mkFC(
		mkHunk(added("a")),
		mkHunk(added("hello")),
	)
	// hunk 0 costs 1; then we need HunkGap (6) before hunk 1
	a := ApplyFile(f, 1)
	if a.HunkIdx != 1 || a.LineIdx != 0 || a.CharsInLine != 0 || a.Done {
		t.Fatalf("got %+v after hunk 0 consumed", a)
	}
	a = ApplyFile(f, 1+HunkGap)
	if a.HunkIdx != 1 || a.LineIdx != 0 || a.CharsInLine != 0 {
		t.Fatalf("got %+v at start of hunk 1", a)
	}
	a = ApplyFile(f, 1+HunkGap+2)
	if a.HunkIdx != 1 || a.LineIdx != 0 || a.CharsInLine != 2 || a.Done {
		t.Fatalf("got %+v inside hunk 1 line 0 typing", a)
	}
}

func TestPartialLine_StopsAtTokenBoundary(t *testing.T) {
	got := PartialLine("func foo()", 4)
	if got != "func" {
		t.Errorf("got %q, want %q", got, "func")
	}
}

func TestPartialLine_BoundsAndFull(t *testing.T) {
	if got := PartialLine("abc", 0); got != "" {
		t.Errorf("zero = %q", got)
	}
	if got := PartialLine("abc", 100); got != "abc" {
		t.Errorf("over = %q", got)
	}
}

func TestFileBudget_HasMinimumFloor(t *testing.T) {
	if got := FileBudget(model.FileChange{}); got < MinFileBudget {
		t.Errorf("empty file budget = %d, want >= %d", got, MinFileBudget)
	}
}

func TestCommitMaxBudget_PicksLargest(t *testing.T) {
	c := model.Commit{Files: []model.FileChange{
		{Hunks: []model.Hunk{{Lines: []model.DiffLine{{Kind: model.LineAdded, Text: "abc"}}}}},                  // 3
		{Hunks: []model.Hunk{{Lines: []model.DiffLine{{Kind: model.LineAdded, Text: "abcdefghijklmnopqrst"}}}}}, // 20
		{Hunks: []model.Hunk{{Lines: []model.DiffLine{{Kind: model.LineAdded, Text: "ab"}}}}},                   // 8 (floor)
	}}
	if got := CommitMaxBudget(c); got != 20 {
		t.Errorf("got %d, want 20 (the largest file)", got)
	}
}

// FirstHunkProfile must restrict budgets to the first hunk so the HTML
// renderer's dwell doesn't include time for invisible later hunks.
func TestFileBudgetWith_FirstHunkProfile_IgnoresLaterHunks(t *testing.T) {
	f := mkFC(
		mkHunk(added("ab")),                        // 2 units
		mkHunk(added("zzzzzzzzzzzzzzzzzzzzzzzzzz")), // would be 26 units if visible
	)
	got := FileBudgetWith(f, FirstHunkProfile)
	// First hunk = 2 units, but MinFileBudget (8) floors it.
	if got != MinFileBudget {
		t.Errorf("first-hunk budget = %d, want %d", got, MinFileBudget)
	}
}

// FirstHunkProfile must also cap lines per hunk: only the first
// VisibleLinesPerHunkHTML lines contribute to the budget.
func TestFileBudgetWith_FirstHunkProfile_CapsLines(t *testing.T) {
	// Build a hunk with VisibleLinesPerHunkHTML+2 added lines, each
	// 4 chars long — so the cap, not the file's content, is what
	// bounds the budget.
	visible := VisibleLinesPerHunkHTML
	lines := make([]struct {
		k model.DiffLineKind
		t string
	}, visible+2)
	for i := range lines {
		lines[i] = added("aaaa")
	}
	long := mkHunk(lines...)
	f := mkFC(long)
	want := visible * 4 // each visible added line costs runeCount("aaaa") = 4
	if got := FileBudgetWith(f, FirstHunkProfile); got != want {
		t.Errorf("budget = %d, want %d (%d × 4 chars)", got, want, visible)
	}
}
