// Package htmlout writes the replay history as a single self-contained
// HTML file. The browser-side player consumes data precomputed by the
// internal/replay package — heat snapshots, per-file budgets, per-commit
// dwell ms — so the JS doesn't reimplement playback policy.
package htmlout

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/S-Nakamur-a/gitfilm/internal/model"
	"github.com/S-Nakamur-a/gitfilm/internal/output"
	"github.com/S-Nakamur-a/gitfilm/internal/replay"
)

//go:embed template.html
var templateHTML string

// DefaultOutputPath is used when output.Config.HTMLOutPath is empty.
const DefaultOutputPath = "gitfilm.html"

// maxHunksHTML matches replay.FirstHunkProfile.MaxHunks: the HTML
// player only animates the first hunk per file. Defining it here as a
// constant lets us slice without a circular reference back to replay's
// VisibilityProfile fields.
const maxHunksHTML = 1

// detailChunkSize is how many commits' detail (files/snapshot/body)
// share a single <script id="chunk-N"> tag. The browser parses one
// chunk on demand when its commit becomes the current frame, so the
// initial <script id="data"> JSON.parse only sees a small "summaries"
// array (a few MB) regardless of total history length. 100 keeps each
// chunk's JSON.parse below ~50ms on a 7.9k-commit monorepo while
// avoiding excessive script-tag overhead.
const detailChunkSize = 100

// chunkMarker splits template.html so we can stream chunk <script>
// tags between the head and the player JS without buffering them all
// in memory before template.Execute. Treated as a literal string.
const chunkMarker = "<!--GITFILM_CHUNKS-->"

// Render writes a single self-contained HTML file representing the
// history. The page embeds all commit data, precomputed playback
// budgets and per-frame heat snapshots as JSON, so the file works
// fully offline and the browser only handles cursor advancement and
// DOM updates.
func Render(w io.Writer, h model.History) error {
	return RenderWithDiag(w, h, nil)
}

// RenderWithDiag is Render plus per-stage timing reports written to
// diag (when non-nil). Used by the CLI to expose where the wall time
// goes when generating the HTML — payload build vs JSON encode vs
// template execute. Keeping it as a separate entry point so existing
// callers / tests don't need to plumb a writer.
//
// Output structure:
//
//	<head>...</head>
//	<body>
//	  ... ui ...
//	  <script id="data">{...meta + summaries...}</script>
//	  <script id="chunk-0">[...detail for commits 0..99...]</script>
//	  <script id="chunk-1">[...detail for commits 100..199...]</script>
//	  ...
//	  <script>player JS</script>
//	</body>
//
// The browser parses only the small "data" script up front. Each
// chunk's textContent is JSON.parse'd lazily when the user scrubs into
// it. This keeps initial paint quick and bounded regardless of how
// many commits the history contains.
func RenderWithDiag(w io.Writer, h model.History, diag io.Writer) error {
	tBuild := time.Now()
	meta, chunks, payloadStats := buildPayloadTimed(h, diag)
	dBuild := time.Since(tBuild)

	head, tail, ok := strings.Cut(templateHTML, chunkMarker)
	if !ok {
		return fmt.Errorf("htmlout: template missing %q marker", chunkMarker)
	}

	tParse := time.Now()
	headTmpl, err := template.New("gitfilm-head").Parse(head)
	if err != nil {
		return err
	}
	dParse := time.Since(tParse)

	// Encode meta (small) into a buffer so we can pass it through
	// html/template as template.JS. Meta is summary-only so this stays
	// in the few-MB range even for huge histories.
	tMeta := time.Now()
	var metaBuf bytes.Buffer
	enc := json.NewEncoder(&metaBuf)
	enc.SetEscapeHTML(true)
	if err := enc.Encode(meta); err != nil {
		return err
	}
	metaStr := strings.TrimRight(metaBuf.String(), "\n")
	dMeta := time.Since(tMeta)

	tExec := time.Now()
	if err := headTmpl.Execute(w, struct {
		Branch  string
		Against string
		Data    template.JS
	}{
		Branch:  h.Branch,
		Against: h.Against,
		Data:    template.JS(metaStr),
	}); err != nil {
		return err
	}
	dExec := time.Since(tExec)

	// Stream detail chunks directly to the writer — no Buffer, so peak
	// memory stays bounded by chunk size, not total payload.
	tChunks := time.Now()
	var chunkBytes int
	cw := &countingWriter{w: w}
	for i, ch := range chunks {
		if _, err := fmt.Fprintf(cw, "\n<script id=\"chunk-%d\" type=\"application/json\">", i); err != nil {
			return err
		}
		ce := json.NewEncoder(cw)
		ce.SetEscapeHTML(true)
		if err := ce.Encode(ch); err != nil {
			return err
		}
		if _, err := io.WriteString(cw, "</script>"); err != nil {
			return err
		}
	}
	chunkBytes = cw.n
	dChunks := time.Since(tChunks)

	tTail := time.Now()
	if _, err := io.WriteString(w, tail); err != nil {
		return err
	}
	dTail := time.Since(tTail)

	if diag != nil {
		fmt.Fprintf(diag,
			"htmlout timings: build=%s (heat=%s, snapshot=%s, files=%s) parse=%s meta=%s exec=%s chunks=%s tail=%s meta_json=%.1f MB chunk_json=%.1f MB commits=%d chunks=%d\n",
			dBuild.Round(time.Millisecond),
			payloadStats.heat.Round(time.Millisecond),
			payloadStats.snapshot.Round(time.Millisecond),
			payloadStats.files.Round(time.Millisecond),
			dParse.Round(time.Millisecond),
			dMeta.Round(time.Millisecond),
			dExec.Round(time.Millisecond),
			dChunks.Round(time.Millisecond),
			dTail.Round(time.Millisecond),
			float64(metaBuf.Len())/1024/1024,
			float64(chunkBytes)/1024/1024,
			len(h.Commits),
			len(chunks),
		)
	}
	return nil
}

// countingWriter tallies bytes written so the diag line can show the
// streamed chunk total without holding it in memory.
type countingWriter struct {
	w io.Writer
	n int
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += n
	return n, err
}

// metaJSON is the eager-parsed payload. It carries everything the
// browser needs for timeline rendering, dwell scheduling and basic
// commit metadata, but nothing per-frame heavy (no diff text, no
// snapshots, no commit body). Heavy fields live in chunked detail
// scripts and are JSON.parse'd on demand.
type metaJSON struct {
	Branch      string          `json:"branch"`
	Against     string          `json:"against"`
	Tuning      tuningJSON      `json:"tuning"`
	CommitCount int             `json:"commit_count"`
	ChunkSize   int             `json:"chunk_size"`
	Summaries   []commitSummary `json:"summaries"`
}

// commitSummary is the lightweight per-commit record present in meta.
// Roughly ~150–250 bytes JSON-encoded, so 7.9k commits ≈ 1–2 MB.
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

// commitDetail is the heavyweight per-commit record. Lives inside a
// chunk script tag and is JSON.parse'd lazily by the player.
type commitDetail struct {
	Body     string       `json:"body,omitempty"`
	Files    []fileJSON   `json:"files"`
	Snapshot snapshotJSON `json:"snapshot"`
}

// tuningJSON exposes the policy constants from internal/replay so the
// browser uses the same values as the Go renderer. Editing the Go
// constants automatically flows through to the HTML output without
// touching template.html.
type tuningJSON struct {
	LineCost      int     `json:"line_cost"`
	HunkGap       int     `json:"hunk_gap"`
	MinFileBudget int     `json:"min_file_budget"`
	VisibleLines  int     `json:"visible_lines"`
	HalfLife      float64 `json:"half_life"`
	HiddenBelow   float64 `json:"hidden_below"`
	FaintBelow    float64 `json:"faint_below"`
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
// f.hunks[0].lines[:VISIBLE_LINES], so the old/new line-number metadata
// and the hunk header are never read on the browser side. Dropping
// them shrinks the per-commit payload — at ~130k hunks in a 7.9k-commit
// monorepo, even a few bytes per hunk multiplies into MB.
type hunkJSON struct {
	Lines []lineJSON `json:"lines"`
}

// Compact line representation: k = "+" / "-" / " ", t = text.
type lineJSON struct {
	K string `json:"k"`
	T string `json:"t"`
}

// snapshotJSON is the per-frame heat map. Encoded as parallel arrays
// rather than {path: heat} objects so the JSON is smaller (no quoted
// keys repeated thousands of times) and order is preserved.
type snapshotJSON struct {
	Paths   []string  `json:"paths"`
	Heat    []float64 `json:"heat"`
	Touches []int     `json:"touches"`
	Ghosts  []string  `json:"ghosts"`
	Added   []string  `json:"added"`
}

// payloadStats accumulates per-stage durations inside buildPayload so
// callers (RenderWithDiag) can report where the time went.
type payloadStats struct {
	heat     time.Duration // state.Step
	snapshot time.Duration // HeatSnapshot + buildSnapshot (sort, copy)
	files    time.Duration // file/hunk/line copy + budget calc
}

// buildPayloadTimed walks the history once, accumulating
//   - meta.Summaries: lightweight per-commit fields parsed eagerly by
//     the browser (hash/subject/dwell/tag/etc.)
//   - chunks: groups of detailChunkSize commitDetail records (body,
//     files, snapshot) parsed lazily on demand.
//
// The split keeps the eager parse bounded by O(commits) summary bytes
// instead of O(commits) × diff-text bytes. Every commit still gets one
// state.Step + one HeatSnapshotWith call, so cost-of-build is the same
// as the single-payload version.
func buildPayloadTimed(h model.History, diag io.Writer) (metaJSON, [][]commitDetail, payloadStats) {
	opts := replay.DefaultSnapshotOpts()
	meta := metaJSON{
		Branch:  h.Branch,
		Against: h.Against,
		Tuning: tuningJSON{
			LineCost:      replay.LineCost,
			HunkGap:       replay.HunkGap,
			MinFileBudget: replay.MinFileBudget,
			VisibleLines:  replay.VisibleLinesPerHunkHTML,
			HalfLife:      replay.DefaultHalfLife,
			HiddenBelow:   opts.HiddenBelow,
			FaintBelow:    opts.FaintBelow,
		},
		CommitCount: len(h.Commits),
		ChunkSize:   detailChunkSize,
		Summaries:   make([]commitSummary, 0, len(h.Commits)),
	}

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
		meta.Summaries = append(meta.Summaries, commitSummary{
			Hash:       c.Hash,
			Short:      c.ShortHash,
			AuthorName: c.AuthorName,
			When:       c.When.Format("2006-01-02 15:04"),
			WhenUnix:   c.When.Unix(),
			Subject:    c.Subject,
			Tag:        tagJSON(c.Tag),
			MaxBudget:  replay.CommitMaxBudgetWith(c, replay.FirstHunkProfile),
			DwellMS:    replay.DwellForWith(c, replay.FirstHunkProfile).Milliseconds(),
		})
		det := commitDetail{
			Body:     c.Body,
			Snapshot: snap,
			// Empty slice (not nil) so JSON encodes [] — JS code reads
			// `c.files.length` and would throw on null.
			Files: make([]fileJSON, 0, len(c.Files)),
		}
		for _, f := range c.Files {
			fj := fileJSON{
				Path:    f.Path,
				Status:  f.Status.String(),
				Added:   f.Added,
				Removed: f.Removed,
				Budget:  replay.FileBudgetWith(f, replay.FirstHunkProfile),
				// Same reason as det.Files: binary / mode-only / empty
				// renames produce zero hunks. Initialize so JSON is [],
				// not null. Without this the player crashed at the first
				// hunkless file (e.g. a merge or binary change) — the
				// rAF loop died and playback froze around 8 commits in.
				Hunks: make([]hunkJSON, 0, maxHunksHTML),
			}
			// Ship only what the player will render. The HTML profile
			// shows hunks[0].lines[:VisibleLinesPerHunkHTML]; everything
			// else is bytes the browser will load and ignore.
			hunks := f.Hunks
			if len(hunks) > maxHunksHTML {
				hunks = hunks[:maxHunksHTML]
			}
			for _, hk := range hunks {
				lines := hk.Lines
				if len(lines) > replay.VisibleLinesPerHunkHTML {
					lines = lines[:replay.VisibleLinesPerHunkHTML]
				}
				hj := hunkJSON{Lines: make([]lineJSON, 0, len(lines))}
				for _, l := range lines {
					hj.Lines = append(hj.Lines, lineJSON{K: lineKind(l.Kind), T: l.Text})
				}
				fj.Hunks = append(fj.Hunks, hj)
			}
			det.Files = append(det.Files, fj)
		}
		curChunk = append(curChunk, det)
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

// buildSnapshot encodes a HeatSnapshot as parallel arrays. Paths /
// heat / touches share index ordering; ghosts and added are flat
// string arrays. We sort paths so the output is byte-stable across
// runs (also helps gzip ratio on the resulting HTML).
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

// renderer adapts htmlout to the output.Renderer interface.
type renderer struct{}

func (renderer) Run(h model.History, cfg output.Config, diag io.Writer) error {
	path := cfg.HTMLOutPath
	if path == "" {
		path = DefaultOutputPath
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	t0 := time.Now()
	if err := RenderWithDiag(f, h, diag); err != nil {
		return err
	}
	if diag != nil {
		st, _ := f.Stat()
		var sizeMB float64
		if st != nil {
			sizeMB = float64(st.Size()) / 1024 / 1024
		}
		fmt.Fprintf(diag, "wrote %s (%d frames, %.1f MB) total=%s\n",
			path, len(h.Commits), sizeMB, time.Since(t0).Round(time.Millisecond))
	}
	return nil
}

func init() {
	output.Register("html", renderer{})
}
