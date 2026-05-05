package gitlog

import (
	"fmt"
	"strings"
	"testing"

	"github.com/S-Nakamur-a/gitplay/internal/model"
)

// fakeRunner answers a few git invocations from canned output.
type fakeRunner struct {
	responses map[string]string
	calls     []string
}

func (f *fakeRunner) Run(args ...string) ([]byte, error) {
	key := strings.Join(args, " ")
	f.calls = append(f.calls, key)
	for prefix, body := range f.responses {
		if strings.HasPrefix(key, prefix) {
			return []byte(body), nil
		}
	}
	return nil, fmt.Errorf("unexpected git invocation: %s", key)
}

// commitChunk returns the bytes that `git log -p --format=...` would
// emit for a single commit using our marker-line format.
func commitChunk(hash, subject, body, diff string) string {
	return strings.Join([]string{
		beginMarker,
		hash,
		hash[:2],
		"alice",
		"a@example.com",
		"1700000000",
		subject,
		bodyOpenMarker,
		body,
		bodyCloseMarker,
	}, "\n") + "\n" + diff
}

func TestLoader_TagsCommitsAtMergeBase(t *testing.T) {
	// History (newest first along first-parent of feat):
	//   F2 (feat)  <- HEAD of feat
	//   F1 (feat)
	//   M2 (main, merge-base)
	//   M1 (main)
	diffFor := func(h string) string {
		return "diff --git a/" + h + ".txt b/" + h + ".txt\n" +
			"new file mode 100644\n" +
			"--- /dev/null\n+++ b/" + h + ".txt\n" +
			"@@ -0,0 +1,1 @@\n" +
			"+hello from " + h + "\n"
	}
	logOutput := commitChunk("F2hash00", "feat: F2", "", diffFor("F2hash00")) +
		commitChunk("F1hash00", "feat: F1", "", diffFor("F1hash00")) +
		commitChunk("M2hash00", "fix: M2", "", diffFor("M2hash00")) +
		commitChunk("M1hash00", "init", "", diffFor("M1hash00"))

	fr := &fakeRunner{responses: map[string]string{
		"rev-list --first-parent feat ^main":   "F2hash00\nF1hash00\n",
		"rev-list --first-parent --count feat": "4\n",
		"log --first-parent --no-color":        logOutput,
	}}

	l := NewLoaderWithRunner(fr)
	hist, err := l.Load(LoadRequest{Branch: "feat", Against: "main"})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(hist.Commits) != 4 {
		t.Fatalf("got %d commits, want 4", len(hist.Commits))
	}

	wantTags := []model.BranchTag{
		model.BranchTagAgainst, // M1
		model.BranchTagAgainst, // M2
		model.BranchTagFeature, // F1
		model.BranchTagFeature, // F2
	}
	wantHashes := []string{"M1hash00", "M2hash00", "F1hash00", "F2hash00"}
	for i, c := range hist.Commits {
		if c.Hash != wantHashes[i] {
			t.Errorf("commits[%d].Hash = %q, want %q", i, c.Hash, wantHashes[i])
		}
		if c.Tag != wantTags[i] {
			t.Errorf("commits[%d].Tag = %v, want %v (hash=%s)", i, c.Tag, wantTags[i], c.Hash)
		}
		if len(c.Files) != 1 {
			t.Errorf("commits[%d] should have 1 file, got %d", i, len(c.Files))
		}
	}
}

func TestLoader_FallbackWhenAgainstUnreachable(t *testing.T) {
	diff := "diff --git a/x.txt b/x.txt\nnew file mode 100644\n--- /dev/null\n+++ b/x.txt\n@@ -0,0 +1,1 @@\n+hi\n"
	logOutput := commitChunk("C1hash00", "subj", "", diff)

	fr := &fakeRunner{responses: map[string]string{
		"log --first-parent --no-color":        logOutput,
		"rev-list --first-parent --count feat": "1\n",
		// rev-list ^against intentionally absent -> error -> all-Feature fallback
	}}

	l := NewLoaderWithRunner(fr)
	hist, err := l.Load(LoadRequest{Branch: "feat", Against: "missing"})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(hist.Commits) != 1 || hist.Commits[0].Tag != model.BranchTagFeature {
		t.Fatalf("expected fallback to all-feature; got %+v", hist.Commits)
	}
}

func TestParseLogP_BodyAndSubjectPreserved(t *testing.T) {
	body := "this is\na multi-line\nbody"
	chunk := commitChunk("aaaaaaa1", "subject line", body, "")

	fr := &fakeRunner{responses: map[string]string{
		"log --first-parent --no-color":        chunk,
		"rev-list --first-parent main ^foo":    "",
		"rev-list --first-parent --count main": "1\n",
	}}
	l := NewLoaderWithRunner(fr)
	hist, err := l.Load(LoadRequest{Branch: "main", Against: "foo"})
	if err != nil {
		t.Fatal(err)
	}
	if len(hist.Commits) != 1 {
		t.Fatalf("got %d commits", len(hist.Commits))
	}
	c := hist.Commits[0]
	if c.Subject != "subject line" {
		t.Errorf("subject = %q", c.Subject)
	}
	if c.Body != body {
		t.Errorf("body = %q, want %q", c.Body, body)
	}
}
