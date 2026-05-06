package replay

import "hash/fnv"

// AuthorPalette is the curated color palette used to give each commit
// author a stable visual identity (the "登場人物" of the film). Picked
// to satisfy three constraints simultaneously:
//
//   - Readable on a dark terminal background (no near-black hues).
//   - Distinct from the heat scale (cool→warm→hot→active runs from
//     blue → yellow → orange → red), so an author accent on the
//     metadata line never reads like a heat tier on a filename.
//   - Distinct enough from the branch-tag colors (feat = pink,
//     against = light blue) that an author chip stays legible even
//     when sitting next to a tag chip.
//
// 10 entries is intentionally small. With more, hues collapse toward
// each other and authors become harder to tell apart at a glance —
// the point is loose per-session identity, not a global UUID. Two
// authors will occasionally share a color on a busy repo; that's the
// accepted tradeoff.
var AuthorPalette = []string{
	"#a6cee3", // light blue
	"#b2df8a", // pale green
	"#fb9a99", // salmon
	"#fdbf6f", // peach
	"#cab2d6", // lavender
	"#fdb462", // amber
	"#b3de69", // light lime
	"#80b1d3", // sky
	"#fccde5", // pink
	"#ffd54f", // mustard
}

// AuthorColor returns the palette entry for a commit author. The
// mapping is a stable hash (FNV-32a) of the name, so the same author
// always lands on the same color across a run and across renders.
//
// Empty names map to the first palette entry rather than erroring —
// missing author metadata in old repos shouldn't break the UI.
func AuthorColor(name string) string {
	if name == "" {
		return AuthorPalette[0]
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(name))
	return AuthorPalette[int(h.Sum32()%uint32(len(AuthorPalette)))]
}
