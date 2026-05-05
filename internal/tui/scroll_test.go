package tui

import "testing"

// TestCardLineWindow_TailsCursor: during typing, the cursor sits at
// the last visible row, so the user always sees the line currently
// being typed plus the immediately-preceding context.
func TestCardLineWindow_TailsCursor(t *testing.T) {
	// Cursor at line 50 of a 100-line hunk, 6-row window.
	start, end, hidden := cardLineWindow(50, 100, 6, false)
	if end != 51 {
		t.Errorf("end = %d, want 51 (cursor + 1)", end)
	}
	// 6 rows total: 1 indicator + 5 diff lines → start = 51 - 5 = 46
	if start != 46 {
		t.Errorf("start = %d, want 46", start)
	}
	if hidden != 46 {
		t.Errorf("hidden = %d, want 46", hidden)
	}
	if end-start != 5 {
		t.Errorf("visible diff rows = %d, want 5 (window 6 minus 1 indicator)", end-start)
	}
}

// TestCardLineWindow_NoScrollSmallHunk: a hunk that fits in the
// window is shown in full, no indicator.
func TestCardLineWindow_NoScrollSmallHunk(t *testing.T) {
	start, end, hidden := cardLineWindow(3, 5, 6, false)
	if start != 0 || end != 4 {
		t.Errorf("start, end = %d, %d; want 0, 4", start, end)
	}
	if hidden != 0 {
		t.Errorf("hidden = %d, want 0", hidden)
	}
}

// TestCardLineWindow_DoneTailsHunkEnd: when the file animation is
// done, we tail the END of the active hunk (the last `visibleRows-1`
// lines plus the indicator).
func TestCardLineWindow_DoneTailsHunkEnd(t *testing.T) {
	start, end, hidden := cardLineWindow(99, 100, 6, true)
	if end != 100 {
		t.Errorf("end = %d, want 100 (tail when done)", end)
	}
	if start != 95 {
		t.Errorf("start = %d, want 95", start)
	}
	if hidden != 95 {
		t.Errorf("hidden = %d, want 95", hidden)
	}
}

// TestCardLineWindow_HunkStart: cursor at line 0 — show the first
// row only (typing has just started, no indicator since nothing is
// hidden above).
func TestCardLineWindow_HunkStart(t *testing.T) {
	start, end, hidden := cardLineWindow(0, 100, 6, false)
	if start != 0 || end != 1 {
		t.Errorf("start, end = %d, %d; want 0, 1", start, end)
	}
	if hidden != 0 {
		t.Errorf("hidden = %d, want 0", hidden)
	}
}

// TestCardLineWindow_DegenerateInputs guards against bad sizes.
func TestCardLineWindow_DegenerateInputs(t *testing.T) {
	if start, end, _ := cardLineWindow(5, 0, 6, false); start != 0 || end != 0 {
		t.Errorf("totalLines=0: got %d,%d; want 0,0", start, end)
	}
	if start, end, _ := cardLineWindow(5, 10, 0, false); start != 0 || end != 0 {
		t.Errorf("visibleRows=0: got %d,%d; want 0,0", start, end)
	}
}
