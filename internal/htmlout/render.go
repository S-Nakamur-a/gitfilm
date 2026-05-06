// Package htmlout writes the replay history as a single
// self-contained HTML file. The browser-side player consumes data
// precomputed by the internal/replay package — heat snapshots,
// per-file budgets, per-commit dwell ms — so the JS doesn't
// reimplement playback policy.
//
// File layout in this package:
//
//   - render.go    template parse + I/O writer + Renderer adapter
//   - payload.go   JSON shapes + buildPayloadTimed walk
//   - template.html  embedded HTML/CSS/JS (player only)
package htmlout

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"os"
	"strings"
	"time"

	"github.com/S-Nakamur-a/gitfilm/internal/model"
	"github.com/S-Nakamur-a/gitfilm/internal/output"
)

//go:embed template.html
var templateHTML string

// DefaultOutputPath is used when output.Config.HTMLOutPath is
// empty.
const DefaultOutputPath = "gitfilm.html"

// chunkMarker splits template.html so we can stream chunk
// <script> tags between the head and the player JS without
// buffering them all in memory before template.Execute. Treated
// as a literal string.
const chunkMarker = "<!--GITFILM_CHUNKS-->"

// RenderOptions carries optional toggles for HTML rendering. Zero
// value = baseline behaviour.
type RenderOptions struct {
	// Scramble turns on the "movie decoder" typing effect in the
	// browser-side player.
	Scramble bool
	// ScrambleAhead is forwarded to the player as
	// tuning.scramble_ahead. Zero falls back to
	// replay.DefaultScrambleAhead.
	ScrambleAhead int
}

// Render writes a single self-contained HTML file representing
// the history. The page embeds all commit data, precomputed
// playback budgets and per-frame heat snapshots as JSON, so the
// file works fully offline and the browser only handles cursor
// advancement and DOM updates.
func Render(w io.Writer, h model.History) error {
	return RenderWithDiag(w, h, nil)
}

// RenderWithDiag is Render plus per-stage timing reports written
// to diag (when non-nil). Used by the CLI to expose where the
// wall time goes when generating the HTML — payload build vs
// JSON encode vs template execute.
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
// chunk's textContent is JSON.parse'd lazily when the user scrubs
// into it. This keeps initial paint quick and bounded regardless
// of how many commits the history contains.
func RenderWithDiag(w io.Writer, h model.History, diag io.Writer) error {
	return RenderWithOptions(w, h, RenderOptions{}, diag)
}

// RenderWithOptions is RenderWithDiag plus opt-in toggles (e.g.
// scramble). The defaults preserve the historical behaviour, so
// existing callers can stay on Render/RenderWithDiag.
func RenderWithOptions(w io.Writer, h model.History, opts RenderOptions, diag io.Writer) error {
	tBuild := time.Now()
	meta, chunks, payload := buildPayloadTimed(h, opts, diag)
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

	tMeta := time.Now()
	metaStr, err := encodeMeta(meta)
	if err != nil {
		return err
	}
	dMeta := time.Since(tMeta)

	tExec := time.Now()
	if err := executeHead(w, headTmpl, h, metaStr); err != nil {
		return err
	}
	dExec := time.Since(tExec)

	tChunks := time.Now()
	chunkBytes, err := streamChunks(w, chunks)
	if err != nil {
		return err
	}
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
			payload.heat.Round(time.Millisecond),
			payload.snapshot.Round(time.Millisecond),
			payload.files.Round(time.Millisecond),
			dParse.Round(time.Millisecond),
			dMeta.Round(time.Millisecond),
			dExec.Round(time.Millisecond),
			dChunks.Round(time.Millisecond),
			dTail.Round(time.Millisecond),
			float64(len(metaStr))/1024/1024,
			float64(chunkBytes)/1024/1024,
			len(h.Commits),
			len(chunks),
		)
	}
	return nil
}

// encodeMeta serializes the eager-parse meta (small) into a
// string we can pass through html/template as template.JS. Meta
// is summary-only so this stays in the few-MB range even for
// huge histories.
func encodeMeta(meta metaJSON) (string, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(true)
	if err := enc.Encode(meta); err != nil {
		return "", err
	}
	return strings.TrimRight(buf.String(), "\n"), nil
}

// executeHead renders the head portion of the template (up to
// chunkMarker) with branch + against + meta JSON.
func executeHead(w io.Writer, t *template.Template, h model.History, meta string) error {
	return t.Execute(w, struct {
		Branch  string
		Against string
		Data    template.JS
	}{
		Branch:  h.Branch,
		Against: h.Against,
		Data:    template.JS(meta),
	})
}

// streamChunks writes each detail chunk as a <script> tag
// directly to w — no Buffer, so peak memory stays bounded by
// chunk size, not total payload. Returns total bytes written
// across all chunk JSON for diag reporting.
func streamChunks(w io.Writer, chunks [][]commitDetail) (int, error) {
	cw := &countingWriter{w: w}
	for i, ch := range chunks {
		if _, err := fmt.Fprintf(cw, "\n<script id=\"chunk-%d\" type=\"application/json\">", i); err != nil {
			return cw.n, err
		}
		ce := json.NewEncoder(cw)
		ce.SetEscapeHTML(true)
		if err := ce.Encode(ch); err != nil {
			return cw.n, err
		}
		if _, err := io.WriteString(cw, "</script>"); err != nil {
			return cw.n, err
		}
	}
	return cw.n, nil
}

// countingWriter tallies bytes written so the diag line can show
// the streamed chunk total without holding it in memory.
type countingWriter struct {
	w io.Writer
	n int
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += n
	return n, err
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
	if err := RenderWithOptions(f, h, RenderOptions{Scramble: cfg.Scramble, ScrambleAhead: cfg.ScrambleAhead}, diag); err != nil {
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
