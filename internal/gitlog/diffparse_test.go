package gitlog

import (
	"testing"

	"github.com/S-Nakamur-a/gitplay/internal/model"
)

const sampleDiff = `diff --git a/src/parser/lexer.go b/src/parser/lexer.go
index abc123..def456 100644
--- a/src/parser/lexer.go
+++ b/src/parser/lexer.go
@@ -1,5 +1,7 @@
 package parser

-import "strings"
+import (
+	"strings"
+	"unicode"
+)

 func tokenize(s string) []string {
diff --git a/src/parser/visitor.go b/src/parser/visitor.go
new file mode 100644
index 0000000..1234567
--- /dev/null
+++ b/src/parser/visitor.go
@@ -0,0 +1,3 @@
+package parser
+
+type Visitor interface{}
diff --git a/old.go b/new.go
similarity index 80%
rename from old.go
rename to new.go
index 1111111..2222222 100644
--- a/old.go
+++ b/new.go
@@ -10,3 +10,4 @@ func A() {
 	doStuff()
 	moreStuff()
 }
+// trailing
diff --git a/gone.go b/gone.go
deleted file mode 100644
index 3333333..0000000
--- a/gone.go
+++ /dev/null
@@ -1,2 +0,0 @@
-package gone
-// removed
`

func TestParseFileDiffs_StatusesAndCounts(t *testing.T) {
	files, err := parseFileDiffs([]byte(sampleDiff), "")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(files) != 4 {
		t.Fatalf("got %d files, want 4: %+v", len(files), files)
	}

	// 1. modified
	f := files[0]
	if f.Path != "src/parser/lexer.go" || f.Status != model.StatusModified {
		t.Errorf("file 0: got path=%q status=%v", f.Path, f.Status)
	}
	if f.Added != 4 || f.Removed != 1 {
		t.Errorf("file 0: added=%d removed=%d, want 4/1", f.Added, f.Removed)
	}
	if len(f.Hunks) != 1 || len(f.Hunks[0].Lines) == 0 {
		t.Errorf("file 0: hunks malformed: %+v", f.Hunks)
	}
	if f.Hunks[0].OldStart != 1 || f.Hunks[0].OldLines != 5 ||
		f.Hunks[0].NewStart != 1 || f.Hunks[0].NewLines != 7 {
		t.Errorf("file 0: hunk range = %+v", f.Hunks[0])
	}

	// 2. added
	f = files[1]
	if f.Path != "src/parser/visitor.go" || f.Status != model.StatusAdded {
		t.Errorf("file 1: got path=%q status=%v", f.Path, f.Status)
	}
	if f.Added != 3 || f.Removed != 0 {
		t.Errorf("file 1: added=%d removed=%d", f.Added, f.Removed)
	}

	// 3. renamed
	f = files[2]
	if f.Path != "new.go" || f.OldPath != "old.go" || f.Status != model.StatusRenamed {
		t.Errorf("file 2: got path=%q oldpath=%q status=%v", f.Path, f.OldPath, f.Status)
	}
	if f.Added != 1 || f.Removed != 0 {
		t.Errorf("file 2: added=%d removed=%d", f.Added, f.Removed)
	}

	// 4. deleted
	f = files[3]
	if f.Path != "gone.go" || f.Status != model.StatusDeleted {
		t.Errorf("file 3: got path=%q status=%v", f.Path, f.Status)
	}
	if f.Added != 0 || f.Removed != 2 {
		t.Errorf("file 3: added=%d removed=%d", f.Added, f.Removed)
	}
}

func TestParseFileDiffs_SubdirFilter(t *testing.T) {
	files, err := parseFileDiffs([]byte(sampleDiff), "src/parser")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("got %d files, want 2 under src/parser", len(files))
	}
	for _, f := range files {
		if !pathUnder(f.Path, "src/parser") {
			t.Errorf("unexpected file %q under filter", f.Path)
		}
	}
}

func TestParseHunkHeader_DefaultCounts(t *testing.T) {
	h, err := parseHunkHeader("@@ -10 +20 @@ ctx")
	if err != nil {
		t.Fatal(err)
	}
	if h.OldStart != 10 || h.OldLines != 1 || h.NewStart != 20 || h.NewLines != 1 {
		t.Errorf("got %+v, want defaults of 1", h)
	}
	if h.Header != "ctx" {
		t.Errorf("header = %q, want %q", h.Header, "ctx")
	}
}

func TestPathUnder(t *testing.T) {
	cases := []struct {
		path, sub string
		want      bool
	}{
		{"src/a.go", "src", true},
		{"src/a.go", "src/", true},
		{"src", "src", true},
		{"src2/a.go", "src", false},
		{"src/a.go", "", true}, // empty subdir = no filter
		{"", "src", false},
		{"", "", false},
	}
	for _, c := range cases {
		if got := pathUnder(c.path, c.sub); got != c.want {
			t.Errorf("pathUnder(%q,%q) = %v, want %v", c.path, c.sub, got, c.want)
		}
	}
}
