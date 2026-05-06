package replay

import "testing"

func TestSparklineGlyphBoundaries(t *testing.T) {
	cases := []struct {
		v    float64
		want rune
	}{
		{-0.5, '▁'},
		{0, '▁'},
		{0.0001, '▁'},
		{0.5, '▅'},
		{1.0, '█'},
		{2.0, '█'},
	}
	for _, c := range cases {
		got := SparklineGlyph(c.v)
		if got != c.want {
			t.Fatalf("SparklineGlyph(%v) = %q, want %q", c.v, got, c.want)
		}
	}
}

func TestDownsampleMaxPreservesPeaks(t *testing.T) {
	// width matches len: identity.
	in := []int{1, 4, 2, 9, 3}
	got := DownsampleMax(in, len(in))
	for i, v := range got {
		if v != float64(in[i]) {
			t.Fatalf("identity bucket %d: got %v want %v", i, v, in[i])
		}
	}

	// Width less than len: max-per-bucket. Buckets for width=2,
	// n=5: [0..2) => max(1,4)=4 ; [2..5) => max(2,9,3)=9.
	out := DownsampleMax(in, 2)
	if len(out) != 2 {
		t.Fatalf("len = %d, want 2", len(out))
	}
	if out[0] != 4 || out[1] != 9 {
		t.Fatalf("got %v, want [4 9]", out)
	}
}

func TestDownsampleMaxEdgeCases(t *testing.T) {
	if DownsampleMax(nil, 5) != nil {
		t.Fatal("nil input should return nil")
	}
	if DownsampleMax([]int{1, 2}, 0) != nil {
		t.Fatal("zero width should return nil")
	}
}

func TestCaretBucket(t *testing.T) {
	// width >= total => identity.
	if CaretBucket(3, 5, 10) != 3 {
		t.Fatalf("identity caret failed")
	}
	// width < total => binned. For idx=4, total=8, width=4:
	// 4*4/8 = 2.
	if got := CaretBucket(4, 8, 4); got != 2 {
		t.Fatalf("binned caret = %d, want 2", got)
	}
	// out-of-range idx clamps to last bucket.
	if got := CaretBucket(99, 8, 4); got != 3 {
		t.Fatalf("clamp caret = %d, want 3", got)
	}
	// nonsense inputs return -1.
	if CaretBucket(0, 0, 5) != -1 {
		t.Fatal("zero total should return -1")
	}
	if CaretBucket(0, 5, 0) != -1 {
		t.Fatal("zero width should return -1")
	}
}
