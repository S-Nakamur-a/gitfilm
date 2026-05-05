package htmlout

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"html/template"
	"io"
	"strings"

	"github.com/S-Nakamur-a/gitplay/internal/model"
)

//go:embed template.html
var templateHTML string

// Render writes a single self-contained HTML file representing the
// history. The page embeds all commit data as JSON in a <script
// type="application/json"> tag and animates it client-side, so the file
// works fully offline.
func Render(w io.Writer, h model.History) error {
	payload := buildPayload(h)
	tmpl, err := template.New("gitplay").Parse(templateHTML)
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
	// trailing newline from Encoder is fine; trim to keep the HTML tidy.
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
	Commits []commitJSON `json:"commits"`
}

type commitJSON struct {
	Hash       string     `json:"hash"`
	Short      string     `json:"short"`
	AuthorName string     `json:"author_name"`
	When       string     `json:"when"`
	Subject    string     `json:"subject"`
	Body       string     `json:"body,omitempty"`
	Tag        string     `json:"tag"`
	Files      []fileJSON `json:"files"`
}

type fileJSON struct {
	Path    string     `json:"path"`
	OldPath string     `json:"old_path,omitempty"`
	Status  string     `json:"status"`
	Added   int        `json:"added"`
	Removed int        `json:"removed"`
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

func buildPayload(h model.History) payload {
	p := payload{Branch: h.Branch, Against: h.Against, Commits: make([]commitJSON, 0, len(h.Commits))}
	for _, c := range h.Commits {
		cj := commitJSON{
			Hash:       c.Hash,
			Short:      c.ShortHash,
			AuthorName: c.AuthorName,
			When:       c.When.Format("2006-01-02 15:04"),
			Subject:    c.Subject,
			Body:       c.Body,
			Tag:        tagJSON(c.Tag),
		}
		for _, f := range c.Files {
			fj := fileJSON{
				Path:    f.Path,
				OldPath: f.OldPath,
				Status:  f.Status.String(),
				Added:   f.Added,
				Removed: f.Removed,
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

