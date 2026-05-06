package htmlout

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/S-Nakamur-a/gitfilm/internal/model"
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
		"<title>gitfilm — feat/x</title>",
		`id="data"`,
		`id="chunk-0"`,
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

func TestRender_MetaAndChunkParse(t *testing.T) {
	var buf bytes.Buffer
	if err := Render(&buf, sampleHistory()); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	// meta
	metaRe := regexp.MustCompile(`(?s)<script id="data" type="application/json">(.*?)</script>`)
	mm := metaRe.FindStringSubmatch(out)
	if len(mm) != 2 {
		t.Fatalf("could not locate data script tag")
	}
	var meta metaJSON
	if err := json.Unmarshal([]byte(mm[1]), &meta); err != nil {
		t.Fatalf("meta is not valid JSON: %v", err)
	}
	if meta.Branch != "feat/x" || meta.CommitCount != 1 || len(meta.Summaries) != 1 {
		t.Errorf("meta mismatch: %+v", meta)
	}
	// detail chunk
	chunkRe := regexp.MustCompile(`(?s)<script id="chunk-0" type="application/json">(.*?)</script>`)
	cm := chunkRe.FindStringSubmatch(out)
	if len(cm) != 2 {
		t.Fatalf("could not locate chunk-0 script tag")
	}
	var chunk []commitDetail
	if err := json.Unmarshal([]byte(cm[1]), &chunk); err != nil {
		t.Fatalf("chunk is not valid JSON: %v", err)
	}
	if len(chunk) != 1 || len(chunk[0].Files) != 1 || len(chunk[0].Files[0].Hunks) != 1 {
		t.Errorf("chunk detail mismatch: %+v", chunk)
	}
}

func TestRender_CinematicSummaryFields(t *testing.T) {
	when := time.Date(2024, 1, 1, 10, 0, 0, 0, time.UTC)
	h := model.History{
		Branch:  "feat/cine",
		Against: "main",
		Commits: []model.Commit{
			{
				Hash: "aaa0000000", ShortHash: "aaa0000", AuthorName: "alice",
				When: when, Subject: "feat: 1", Tag: model.BranchTagFeature,
				Files: []model.FileChange{{Path: "a.go", Added: 5, Removed: 2}},
			},
			{ // banner-tier gap (≥ 7 days) so we exercise both gap_tier and BannerExtraDwell
				Hash: "bbb0000000", ShortHash: "bbb0000", AuthorName: "bob",
				When: when.Add(30 * 24 * time.Hour), Subject: "feat: 2", Tag: model.BranchTagFeature,
				Files: []model.FileChange{{Path: "b.go", Added: 3, Removed: 1}},
			},
		},
	}
	var buf bytes.Buffer
	if err := Render(&buf, h); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	metaRe := regexp.MustCompile(`(?s)<script id="data" type="application/json">(.*?)</script>`)
	mm := metaRe.FindStringSubmatch(out)
	if len(mm) != 2 {
		t.Fatalf("could not locate data script tag")
	}
	var meta metaJSON
	if err := json.Unmarshal([]byte(mm[1]), &meta); err != nil {
		t.Fatalf("meta JSON: %v", err)
	}
	if len(meta.Summaries) != 2 {
		t.Fatalf("expected 2 summaries, got %d", len(meta.Summaries))
	}
	s0 := meta.Summaries[0]
	s1 := meta.Summaries[1]
	if s0.AuthorColor == "" || s1.AuthorColor == "" {
		t.Errorf("author_color must be populated, got %q / %q", s0.AuthorColor, s1.AuthorColor)
	}
	if s0.AuthorColor == s1.AuthorColor {
		// Best-effort check: alice/bob differ in the 10-color palette.
		t.Errorf("alice & bob landed on same author color %q", s0.AuthorColor)
	}
	if s0.GapMS != 0 || s0.GapTier != "" {
		t.Errorf("first commit should have empty gap, got ms=%d tier=%q", s0.GapMS, s0.GapTier)
	}
	if s1.GapTier != "banner" {
		t.Errorf("30-day gap should classify as banner, got %q", s1.GapTier)
	}
	if s1.GapMS <= 0 {
		t.Errorf("gap_ms should be positive, got %d", s1.GapMS)
	}
	if s0.Added != 5 || s0.Removed != 2 || s1.Added != 3 || s1.Removed != 1 {
		t.Errorf("added/removed not propagated to summary: %+v %+v", s0, s1)
	}
	// BannerExtraDwell adds 800ms; baseline DwellForWith caps at MaxCommitMS.
	// We can't check exact dwell without re-deriving it, but s1 should be
	// strictly greater than the same commit without a banner gap.
	if s1.DwellMS <= s0.DwellMS {
		t.Errorf("banner-tier commit dwell (%d) should exceed non-banner (%d)", s1.DwellMS, s0.DwellMS)
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
