package htmlout

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/S-Nakamur-a/gitplay/internal/model"
)

func sampleHistory() model.History {
	when := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	return model.History{
		Branch:  "feat/x",
		Against: "main",
		Commits: []model.Commit{
			{
				Hash: "abc1234567", ShortHash: "abc1234", AuthorName: "alice",
				When: when, Subject: "feat: add visitor", Tag: model.BranchTagFeature,
				Files: []model.FileChange{{
					Path: "src/visitor.go", Status: model.StatusAdded, Added: 2,
					Hunks: []model.Hunk{{
						OldStart: 0, OldLines: 0, NewStart: 1, NewLines: 2,
						Lines: []model.DiffLine{
							{Kind: model.LineAdded, Text: "package x"},
							{Kind: model.LineAdded, Text: "type V interface{}"},
						},
					}},
				}},
			},
		},
	}
}

func TestRender_ContainsExpectedMarkers(t *testing.T) {
	var buf bytes.Buffer
	if err := Render(&buf, sampleHistory()); err != nil {
		t.Fatalf("Render: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"<title>gitplay — feat/x</title>",
		`id="data"`,
		`"branch":"feat/x"`,
		`"against":"main"`,
		`"hash":"abc1234567"`,
		`"author_name":"alice"`,
		`"subject":"feat: add visitor"`,
		`"tag":"feat"`,
		`"path":"src/visitor.go"`,
		`"k":"+"`,
		`"t":"package x"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q", want)
		}
	}
}

func TestRender_JSONScriptParses(t *testing.T) {
	var buf bytes.Buffer
	if err := Render(&buf, sampleHistory()); err != nil {
		t.Fatal(err)
	}
	// extract the JSON between the script tags
	re := regexp.MustCompile(`(?s)<script id="data" type="application/json">(.*?)</script>`)
	m := re.FindStringSubmatch(buf.String())
	if len(m) != 2 {
		t.Fatalf("could not locate data script tag")
	}
	var p payload
	if err := json.Unmarshal([]byte(m[1]), &p); err != nil {
		t.Fatalf("data is not valid JSON: %v", err)
	}
	if p.Branch != "feat/x" || len(p.Commits) != 1 {
		t.Errorf("payload mismatch: %+v", p)
	}
	if len(p.Commits[0].Files) != 1 || len(p.Commits[0].Files[0].Hunks) != 1 {
		t.Errorf("files/hunks not preserved: %+v", p.Commits)
	}
}

func TestRender_EscapesScriptTagInjection(t *testing.T) {
	h := sampleHistory()
	// inject a hostile commit subject containing a closing script tag
	h.Commits[0].Subject = `</script><script>alert(1)</script>`
	var buf bytes.Buffer
	if err := Render(&buf, h); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), `</script><script>alert(1)</script>`) {
		t.Errorf("raw script tag injection survived escaping")
	}
}
