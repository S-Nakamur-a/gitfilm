package gitlog

import (
	"strings"
	"testing"

	"github.com/S-Nakamur-a/gitfilm/internal/model"
)

// TestLoadStream_DeliversOldestFirst verifies that batches arrive in
// oldest→newest commit order even though shards are populated from
// newest-first git log windows.
func TestLoadStream_DeliversOldestFirst(t *testing.T) {
	diff := func(h string) string {
		return "diff --git a/" + h + ".txt b/" + h + ".txt\n" +
			"new file mode 100644\n" +
			"--- /dev/null\n+++ b/" + h + ".txt\n" +
			"@@ -0,0 +1,1 @@\n" +
			"+hello from " + h + "\n"
	}
	// 3 commits, newest first as git would emit them.
	chunks := commitChunk("C3hash00", "third", "", diff("C3hash00")) +
		commitChunk("C2hash00", "second", "", diff("C2hash00")) +
		commitChunk("C1hash00", "first", "", diff("C1hash00"))

	fr := &fakeRunner{responses: map[string]string{
		"rev-list --first-parent --count main":  "3\n",
		"rev-list --first-parent main ^against": "",
		"log --first-parent --no-color":         chunks,
	}}
	l := NewLoaderWithRunner(fr)

	ch := l.LoadStream(LoadRequest{Branch: "main", Against: "against"})
	var got []model.Commit
	var sawDone bool
	for b := range ch {
		if b.Err != nil {
			t.Fatalf("stream error: %v", b.Err)
		}
		got = append(got, b.Commits...)
		if b.Done {
			sawDone = true
		}
	}
	if !sawDone {
		t.Errorf("expected a final batch with Done=true")
	}
	if len(got) != 3 {
		t.Fatalf("got %d commits, want 3", len(got))
	}
	wantHashOrder := []string{"C1hash00", "C2hash00", "C3hash00"}
	for i, c := range got {
		if c.Hash != wantHashOrder[i] {
			t.Errorf("commits[%d].Hash = %q, want %q", i, c.Hash, wantHashOrder[i])
		}
	}
}

func TestLoadStream_EmptyHistory(t *testing.T) {
	fr := &fakeRunner{responses: map[string]string{
		"rev-list --first-parent --count main":  "0\n",
		"rev-list --first-parent main ^against": "",
	}}
	l := NewLoaderWithRunner(fr)
	ch := l.LoadStream(LoadRequest{Branch: "main", Against: "against"})
	var batches []LoadBatch
	for b := range ch {
		batches = append(batches, b)
	}
	if len(batches) != 1 {
		t.Fatalf("got %d batches, want 1 (just the Done sentinel)", len(batches))
	}
	if !batches[0].Done || batches[0].Err != nil || len(batches[0].Commits) != 0 {
		t.Errorf("expected Done=true with no commits and no error: %+v", batches[0])
	}
}

func TestLoadStream_PropagatesError(t *testing.T) {
	// rev-list --count returns garbage so commitCount fails.
	fr := &fakeRunner{responses: map[string]string{
		"rev-list --first-parent --count main":  "not-a-number\n",
		"rev-list --first-parent main ^against": "",
	}}
	l := NewLoaderWithRunner(fr)
	ch := l.LoadStream(LoadRequest{Branch: "main", Against: "against"})
	var last LoadBatch
	for b := range ch {
		last = b
	}
	if !last.Done || last.Err == nil {
		t.Errorf("expected Done=true with Err set, got %+v", last)
	}
	if !strings.Contains(last.Err.Error(), "rev-list") {
		t.Errorf("error message should reference rev-list, got %q", last.Err.Error())
	}
}
