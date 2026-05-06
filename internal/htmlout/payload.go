package htmlout

import (
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/S-Nakamur-a/gitfilm/internal/model"
	"github.com/S-Nakamur-a/gitfilm/internal/replay"
)

// maxHunksHTML mirrors replay.FirstHunkProfile.MaxHunks: the HTML
// player only animates the first hunk per file. Defining it here
// as a local constant lets us slice without a circular reference
// back to replay's VisibilityProfile fields.
const maxHunksHTML = 1

// detailChunkSize is how many commits' detail (files/snapshot/
// body) share a single <script id="chunk-N"> tag. The browser
// parses one chunk on demand when its commit becomes the current
// frame, so the initial JSON.parse only sees a small "summaries"
// array (a few MB) regardless of total history length. 100 keeps
// each chunk's parse below ~50ms on a 7.9k-commit monorepo while
// avoiding excessive script-tag overhead.
const detailChunkSize = 100

// metaJSON is the eager-parsed payload. It carries everything the
// browser needs for timeline rendering, dwell scheduling and
// basic commit metadata, but nothing per-frame heavy (no diff
// text, no snapshots, no commit body). Heavy fields live in
// chunked detail scripts and are JSON.parse'd on demand.
type metaJSON struct {
	Branch      string          `json:"branch"`
	Against     string          `json:"against"`
	Tuning      tuningJSON      `json:"tuning"`
	CommitCount int             `json:"commit_count"`
	ChunkSize   int             `json:"chunk_size"`
	Summaries   []commitSummary `json:"summaries"`
}

// commitSummary is the lightweight per-commit record present in
// meta. Roughly 150–250 bytes JSON-encoded, so 7.9k commits
// ≈ 1–2 MB.
type commitSummary struct {
	Hash       string `json:"hash"`
	Short      string `json:"short"`
	AuthorName string `json:"author_name"`
	When       string `json:"when"`
	WhenUnix   int64  `json:"when_unix"`
	Subject    string `json:"subject"`
	Tag        string `json:"tag"`
	DwellMS    int64  `json:"dwell_ms"`
	MaxBudget  int    `json:"max_budget"`
}

// commitDetail is the heavyweight per-commit record. Lives inside
// a chunk script tag and is JSON.parse'd lazily by the player.
type commitDetail struct {
	Body     string       `json:"body,omitempty"`
	Files    []fileJSON   `json:"files"`
	Snapshot snapshotJSON `json:"snapshot"`
}

// tuningJSON exposes the policy constants from internal/replay so
// the browser uses the same values as the Go renderer. Editing
// the Go constants automatically flows through to the HTML output
// without touching template.html.
type tuningJSON struct {
	LineCost        int     `json:"line_cost"`
	HunkGap         int     `json:"hunk_gap"`
	MinFileBudget   int     `json:"min_file_budget"`
	VisibleLines    int     `json:"visible_lines"`
	HalfLife        float64 `json:"half_life"`
	HiddenBelow     float64 `json:"hidden_below"`
	FaintBelow      float64 `json:"faint_below"`
	Scramble        bool    `json:"scramble"`
	ScrambleAhead   int     `json:"scramble_ahead"`
	ScrambleCharset string  `json:"scramble_charset,omitempty"`
}

type fileJSON struct {
	Path    string     `json:"path"`
	Status  string     `json:"status"`
	Added   int        `json:"added"`
	Removed int        `json:"removed"`
	Budget  int        `json:"budget"`
	Hunks   []hunkJSON `json:"hunks"`
}

// hunkJSON is intentionally minimal: the HTML player only renders
// f.hunks[0].lines[:VISIBLE_LINES], so the old/new line-number
// metadata and the hunk header are never read on the browser
// side. Dropping them shrinks the per-commit payload — at ~130k
// hunks in a 7.9k-commit monorepo, even a few bytes per hunk
// multiplies into MB.
type hunkJSON struct {
	Lines []lineJSON `json:"lines"`
}

// lineJSON is the compact line representation: K = "+"/"-"/" ",
// T = text.
type lineJSON struct {
	K string `json:"k"`
	T string `json:"t"`
}

// snapshotJSON is the per-frame heat map. Encoded as parallel
// arrays rather than {path: heat} objects so the JSON is smaller
// (no quoted keys repeated thousands of times) and order is
// preserved.
type snapshotJSON struct {
	Paths   []string  `json:"paths"`
	Heat    []float64 `json:"heat"`
	Touches []int     `json:"touches"`
	Ghosts  []string  `json:"ghosts"`
	Added   []string  `json:"added"`
}

// payloadStats accumulates per-stage durations inside
// buildPayloadTimed so RenderWithDiag can report where the time
// went.
type payloadStats struct {
	heat     time.Duration // state.Step
	snapshot time.Duration // HeatSnapshot + buildSnapshot (sort, copy)
	files    time.Duration // file/hunk/line copy + budget calc
}

// buildPayloadTimed walks the history once, accumulating:
//   - meta.Summaries: lightweight per-commit fields parsed
//     eagerly by the browser (hash/subject/dwell/tag/etc.)
//   - chunks: groups of detailChunkSize commitDetail records
//     (body, files, snapshot) parsed lazily on demand.
//
// The split keeps the eager parse bounded by O(commits) summary
// bytes instead of O(commits) × diff-text bytes. Every commit
// still gets one state.Step + one HeatSnapshotWith call, so
// cost-of-build is the same as the single-payload version.
func buildPayloadTimed(h model.History, ropts RenderOptions, diag io.Writer) (metaJSON, [][]commitDetail, payloadStats) {
	opts := replay.DefaultSnapshotOpts()
	meta := newMeta(h, opts, ropts)
	chunks := make([][]commitDetail, 0, (len(h.Commits)+detailChunkSize-1)/detailChunkSize)
	var curChunk []commitDetail

	var stats payloadStats
	state := replay.NewTreeState(replay.DefaultHalfLife)
	progressEvery := len(h.Commits) / 20
	if progressEvery < 100 {
		progressEvery = 100
	}
	tProgress := time.Now()

	for i, c := range h.Commits {
		ts := time.Now()
		state.Step(c)
		stats.heat += time.Since(ts)

		ts = time.Now()
		snap := buildSnapshot(state.HeatSnapshotWith(opts))
		stats.snapshot += time.Since(ts)

		if diag != nil && (i+1)%progressEvery == 0 {
			fmt.Fprintf(diag,
				"[gitfilm] payload: %d/%d commits (heat=%s snapshot=%s files=%s last-batch=%s heat-entries=%d chunks=%d)\n",
				i+1, len(h.Commits),
				stats.heat.Round(time.Millisecond),
				stats.snapshot.Round(time.Millisecond),
				stats.files.Round(time.Millisecond),
				time.Since(tProgress).Round(time.Millisecond),
				len(snap.Paths),
				len(chunks),
			)
			tProgress = time.Now()
		}

		ts = time.Now()
		meta.Summaries = append(meta.Summaries, commitSummaryFor(c))
		curChunk = append(curChunk, commitDetailFor(c, snap))
		if len(curChunk) >= detailChunkSize {
			chunks = append(chunks, curChunk)
			curChunk = nil
		}
		stats.files += time.Since(ts)
	}
	if len(curChunk) > 0 {
		chunks = append(chunks, curChunk)
	}
	return meta, chunks, stats
}

func newMeta(h model.History, opts replay.SnapshotOpts, ropts RenderOptions) metaJSON {
	tuning := tuningJSON{
		LineCost:      replay.LineCost,
		HunkGap:       replay.HunkGap,
		MinFileBudget: replay.MinFileBudget,
		VisibleLines:  replay.VisibleLinesPerHunkHTML,
		HalfLife:      replay.DefaultHalfLife,
		HiddenBelow:   opts.HiddenBelow,
		FaintBelow:    opts.FaintBelow,
	}
	if ropts.Scramble {
		ahead := ropts.ScrambleAhead
		if ahead <= 0 {
			ahead = replay.DefaultScrambleAhead
		}
		tuning.Scramble = true
		tuning.ScrambleAhead = ahead
		tuning.ScrambleCharset = string(replay.DefaultScrambleCharset)
	}
	return metaJSON{
		Branch:      h.Branch,
		Against:     h.Against,
		Tuning:      tuning,
		CommitCount: len(h.Commits),
		ChunkSize:   detailChunkSize,
		Summaries:   make([]commitSummary, 0, len(h.Commits)),
	}
}

func commitSummaryFor(c model.Commit) commitSummary {
	return commitSummary{
		Hash:       c.Hash,
		Short:      c.ShortHash,
		AuthorName: c.AuthorName,
		When:       c.When.Format("2006-01-02 15:04"),
		WhenUnix:   c.When.Unix(),
		Subject:    c.Subject,
		Tag:        tagJSON(c.Tag),
		MaxBudget:  replay.CommitMaxBudgetWith(c, replay.FirstHunkProfile),
		DwellMS:    replay.DwellForWith(c, replay.FirstHunkProfile).Milliseconds(),
	}
}

func commitDetailFor(c model.Commit, snap snapshotJSON) commitDetail {
	det := commitDetail{
		Body:     c.Body,
		Snapshot: snap,
		// Empty slice (not nil) so JSON encodes [] — JS reads
		// `c.files.length` and would throw on null.
		Files: make([]fileJSON, 0, len(c.Files)),
	}
	for _, f := range c.Files {
		det.Files = append(det.Files, fileJSONFor(f))
	}
	return det
}

func fileJSONFor(f model.FileChange) fileJSON {
	fj := fileJSON{
		Path:    f.Path,
		Status:  f.Status.String(),
		Added:   f.Added,
		Removed: f.Removed,
		Budget:  replay.FileBudgetWith(f, replay.FirstHunkProfile),
		// Initialize so JSON is [], not null. Without this the
		// player crashed at the first hunkless file (e.g. a merge
		// or binary change) — the rAF loop died and playback
		// froze around 8 commits in.
		Hunks: make([]hunkJSON, 0, maxHunksHTML),
	}
	hunks := f.Hunks
	if len(hunks) > maxHunksHTML {
		hunks = hunks[:maxHunksHTML]
	}
	for _, hk := range hunks {
		fj.Hunks = append(fj.Hunks, hunkJSONFor(hk))
	}
	return fj
}

func hunkJSONFor(hk model.Hunk) hunkJSON {
	lines := hk.Lines
	if len(lines) > replay.VisibleLinesPerHunkHTML {
		lines = lines[:replay.VisibleLinesPerHunkHTML]
	}
	hj := hunkJSON{Lines: make([]lineJSON, 0, len(lines))}
	for _, l := range lines {
		hj.Lines = append(hj.Lines, lineJSON{K: lineKind(l.Kind), T: l.Text})
	}
	return hj
}

// buildSnapshot encodes a HeatSnapshot as parallel arrays. Paths
// / heat / touches share index ordering; ghosts and added are
// flat string arrays. Sorting paths makes output byte-stable
// across runs (also helps gzip ratio on the resulting HTML).
func buildSnapshot(hs replay.HeatSnapshot) snapshotJSON {
	out := snapshotJSON{
		Paths:   make([]string, 0, len(hs.Heat)),
		Heat:    make([]float64, 0, len(hs.Heat)),
		Touches: make([]int, 0, len(hs.Heat)),
	}
	for path := range hs.Heat {
		out.Paths = append(out.Paths, path)
	}
	sort.Strings(out.Paths)
	for _, p := range out.Paths {
		out.Heat = append(out.Heat, hs.Heat[p])
		out.Touches = append(out.Touches, hs.Touches[p])
	}
	for path := range hs.Ghosts {
		out.Ghosts = append(out.Ghosts, path)
	}
	sort.Strings(out.Ghosts)
	for path := range hs.Added {
		out.Added = append(out.Added, path)
	}
	sort.Strings(out.Added)
	return out
}

func tagJSON(t model.BranchTag) string {
	switch t {
	case model.BranchTagFeature:
		return "feat"
	case model.BranchTagAgainst:
		return "against"
	default:
		return "unknown"
	}
}

func lineKind(k model.DiffLineKind) string {
	switch k {
	case model.LineAdded:
		return "+"
	case model.LineRemoved:
		return "-"
	default:
		return " "
	}
}
