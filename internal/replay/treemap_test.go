package replay

import (
	"math"
	"testing"
)

func TestTreemapLayout_TotalAreaPreserved(t *testing.T) {
	items := []TreemapItem{
		{"a", 6}, {"b", 6}, {"c", 4}, {"d", 3}, {"e", 2}, {"f", 2}, {"g", 1},
	}
	rs := TreemapLayout(items, 600, 400)
	if len(rs) != len(items) {
		t.Fatalf("expected %d rects, got %d", len(items), len(rs))
	}
	totalArea := 0.0
	for _, r := range rs {
		totalArea += r.W * r.H
	}
	want := 600.0 * 400.0
	if math.Abs(totalArea-want) > 1e-6 {
		t.Errorf("total area %f != target %f", totalArea, want)
	}
}

func TestTreemapLayout_NoOverlap(t *testing.T) {
	// Tile grid sanity: no two rectangles overlap and all stay
	// within the bounding box. We don't enforce a specific layout
	// (squarified leaves room for variation as the algorithm
	// evolves) but no-overlap is a hard invariant.
	items := []TreemapItem{
		{"a", 9}, {"b", 4}, {"c", 4}, {"d", 2}, {"e", 1}, {"f", 1}, {"g", 0.5},
	}
	const W, H = 100.0, 60.0
	rs := TreemapLayout(items, W, H)
	for i, r := range rs {
		if r.X < -1e-6 || r.Y < -1e-6 || r.X+r.W > W+1e-6 || r.Y+r.H > H+1e-6 {
			t.Errorf("rect %d (%s) escapes bounds: x=%f y=%f w=%f h=%f", i, r.Key, r.X, r.Y, r.W, r.H)
		}
		for j := i + 1; j < len(rs); j++ {
			s := rs[j]
			if r.X < s.X+s.W-1e-6 && s.X < r.X+r.W-1e-6 && r.Y < s.Y+s.H-1e-6 && s.Y < r.Y+r.H-1e-6 {
				t.Errorf("rects %s and %s overlap", r.Key, s.Key)
			}
		}
	}
}

func TestTreemapLayout_DescendingOrderPreservesArea(t *testing.T) {
	// Areas should be proportional to weights, regardless of layout
	// shape. Verify by computing area/weight ratios; they should
	// all match within float epsilon.
	items := []TreemapItem{{"a", 7}, {"b", 5}, {"c", 3}, {"d", 1}}
	rs := TreemapLayout(items, 100, 80)
	areaByKey := map[string]float64{}
	for _, r := range rs {
		areaByKey[r.Key] = r.W * r.H
	}
	first := areaByKey["a"] / 7
	for _, it := range items {
		got := areaByKey[it.Key] / it.Weight
		if math.Abs(got-first) > 1e-6 {
			t.Errorf("%s area/weight = %f, want %f", it.Key, got, first)
		}
	}
}

func TestTreemapLayout_DeterministicForEqualWeights(t *testing.T) {
	// Equal-weight items must land in the same rectangles across
	// calls — otherwise the treemap shimmers frame-to-frame as map
	// iteration order leaks. SliceStable inside TreemapLayout
	// preserves *input* order among ties, so as long as callers
	// pass items in a deterministic order, layout is deterministic.
	items := []TreemapItem{
		{"a", 5}, {"b", 5}, {"c", 5}, {"d", 5}, {"e", 5},
	}
	first := TreemapLayout(items, 100, 60)
	for trial := 0; trial < 5; trial++ {
		got := TreemapLayout(items, 100, 60)
		if len(got) != len(first) {
			t.Fatalf("length differs across calls")
		}
		for i := range got {
			if got[i] != first[i] {
				t.Fatalf("rect %d differs across calls: %+v vs %+v", i, first[i], got[i])
			}
		}
	}
}

func TestTreemapLayout_DegenerateInputs(t *testing.T) {
	if rs := TreemapLayout(nil, 100, 80); rs != nil {
		t.Errorf("nil items should return nil, got %v", rs)
	}
	if rs := TreemapLayout([]TreemapItem{{"a", 5}}, 0, 80); rs != nil {
		t.Errorf("zero width should return nil")
	}
	if rs := TreemapLayout([]TreemapItem{{"a", -1}, {"b", 0}}, 100, 80); rs != nil {
		t.Errorf("non-positive weights should return nil, got %v", rs)
	}
	rs := TreemapLayout([]TreemapItem{{"a", 5}, {"b", 0}, {"c", -3}}, 100, 80)
	if len(rs) != 1 || rs[0].Key != "a" {
		t.Errorf("expected single rect for the only positive weight, got %v", rs)
	}
}
