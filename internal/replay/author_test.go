package replay

import (
	"slices"
	"testing"
)

func TestAuthorColor_Stable(t *testing.T) {
	a := AuthorColor("Alice")
	b := AuthorColor("Alice")
	if a != b {
		t.Fatalf("expected stable color for same name, got %s vs %s", a, b)
	}
}

func TestAuthorColor_DifferentNamesDiffer(t *testing.T) {
	// We can't guarantee distinct colors for any two names with a
	// 10-entry palette, but a handful of common names should cover
	// most of the palette. Confirm we hit at least 3 distinct entries.
	names := []string{"Alice", "Bob", "Carol", "Dave", "Eve", "Frank", "Grace", "Heidi"}
	seen := map[string]struct{}{}
	for _, n := range names {
		seen[AuthorColor(n)] = struct{}{}
	}
	if len(seen) < 3 {
		t.Fatalf("expected ≥3 distinct colors across %d names, got %d", len(names), len(seen))
	}
}

func TestAuthorColor_Empty(t *testing.T) {
	if AuthorColor("") != AuthorPalette[0] {
		t.Fatalf("empty author should map to palette[0]")
	}
}

func TestAuthorColor_AlwaysInPalette(t *testing.T) {
	for _, n := range []string{"", "Alice", "東京太郎", "x@example.com", "very long author name with spaces"} {
		if !slices.Contains(AuthorPalette, AuthorColor(n)) {
			t.Fatalf("AuthorColor(%q) returned %q which is not in AuthorPalette", n, AuthorColor(n))
		}
	}
}
