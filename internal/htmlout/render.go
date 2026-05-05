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

	"github.com/S-Nakamur-a/gitfilm/internal/model"
	"github.com/S-Nakamur-a/gitfilm/internal/output"
	"github.com/S-Nakamur-a/gitfilm/internal/replay"
)

//go:embed template.html
var templateHTML string

// DefaultOutputPath is used when output.Config.HTMLOutPath is empty.
const DefaultOutputPath = "gitfilm.html"

// Render writes a single self-contained HTML file representing the
// history. The page embeds all commit data, precomputed playback
// budgets and per-frame heat snapshots as JSON, so the file works
// fully offline and the browser only handles cursor advancement and
// DOM updates.
func Render(w io.Writer, h model.History) error {
	payload := buildPayload(h)
	tmpl, err := template.New("gitfilm").Parse(templateHTML)
	if err != nil {
		return err
	}
	// JSON inside a <script> tag must avoid `</script>` and use safe
	// characters. encoding/json handles `<`/`>`/`&` via SetEscapeHTML.
	var jsonBuf bytes.Buffer
	enc := json.NewEncoder(&jsonBuf)
	enc.SetEscapeHTML(true)
	if err := enc.Encode(payload); err != nil {
		return err
	}
	jsonStr := strings.TrimRight(jsonBuf.String(), "\n")
	return tmpl.Execute(w, struct {
		Branch  string
		Against string
		Data    template.JS
	}{
		Branch:  h.Branch,
		Against: h.Against,
		Data:    template.JS(jsonStr),
	})
}

type payload struct {
	Branch  string       `json:"branch"`
	Against string       `json:"against"`
	Tuning  tuningJSON   `json:"tuning"`
	Commits []commitJSON `json:"commits"`
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

type commitJSON struct {
	Hash       string         `json:"hash"`
	Short      string         `json:"short"`
	AuthorName string         `json:"author_name"`
	When       string         `json:"when"`
	WhenUnix   int64          `json:"when_unix"`
	Subject    string         `json:"subject"`
	Body       string         `json:"body,omitempty"`
	Tag        string         `json:"tag"`
	Files      []fileJSON     `json:"files"`
	DwellMS    int64          `json:"dwell_ms"`
	MaxBudget  int            `json:"max_budget"`
	Snapshot   snapshotJSON   `json:"snapshot"`
}

type fileJSON struct {
	Path    string     `json:"path"`
	OldPath string     `json:"old_path,omitempty"`
	Status  string     `json:"status"`
	Added   int        `json:"added"`
	Removed int        `json:"removed"`
	Budget  int        `json:"budget"`
	Hunks   []hunkJSON `json:"hunks"`
}

type hunkJSON struct {
	OldStart int        `json:"old_start"`
	OldLines int        `json:"old_lines"`
	NewStart int        `json:"new_start"`
	NewLines int        `json:"new_lines"`
	Header   string     `json:"header"`
	Lines    []lineJSON `json:"lines"`
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

func buildPayload(h model.History) payload {
	opts := replay.DefaultSnapshotOpts()
	p := payload{
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
		Commits: make([]commitJSON, 0, len(h.Commits)),
	}

	state := replay.NewTreeState(replay.DefaultHalfLife)
	for _, c := range h.Commits {
		state.Step(c)
		cj := commitJSON{
			Hash:       c.Hash,
			Short:      c.ShortHash,
			AuthorName: c.AuthorName,
			When:       c.When.Format("2006-01-02 15:04"),
			WhenUnix:   c.When.Unix(),
			Subject:    c.Subject,
			Body:       c.Body,
			Tag:        tagJSON(c.Tag),
			MaxBudget:  replay.CommitMaxBudgetWith(c, replay.FirstHunkProfile),
			DwellMS:    replay.DwellForWith(c, replay.FirstHunkProfile).Milliseconds(),
			Snapshot:   buildSnapshot(state.HeatSnapshot()),
		}
		for _, f := range c.Files {
			fj := fileJSON{
				Path:    f.Path,
				OldPath: f.OldPath,
				Status:  f.Status.String(),
				Added:   f.Added,
				Removed: f.Removed,
				Budget:  replay.FileBudgetWith(f, replay.FirstHunkProfile),
			}
			for _, hk := range f.Hunks {
				hj := hunkJSON{
					OldStart: hk.OldStart,
					OldLines: hk.OldLines,
					NewStart: hk.NewStart,
					NewLines: hk.NewLines,
					Header:   hk.Header,
				}
				for _, l := range hk.Lines {
					hj.Lines = append(hj.Lines, lineJSON{K: lineKind(l.Kind), T: l.Text})
				}
				fj.Hunks = append(fj.Hunks, hj)
			}
			cj.Files = append(cj.Files, fj)
		}
		p.Commits = append(p.Commits, cj)
	}
	return p
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
	if err := Render(f, h); err != nil {
		return err
	}
	if diag != nil {
		fmt.Fprintf(diag, "wrote %s (%d frames)\n", path, len(h.Commits))
	}
	return nil
}

func init() {
	output.Register("html", renderer{})
}
