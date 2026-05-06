package gitlog

import (
	"strings"
	"testing"
)

// stdinFakeRunner extends fakeRunner with stdin awareness so seed
// tests can verify what got piped to git check-attr.
type stdinFakeRunner struct {
	fakeRunner
	lastStdin []byte
}

func (s *stdinFakeRunner) RunStdin(stdin []byte, args ...string) ([]byte, error) {
	s.lastStdin = append([]byte(nil), stdin...)
	return s.Run(args...)
}

func TestSplitCheckAttr(t *testing.T) {
	cases := []struct {
		line            string
		path, attr, val string
		ok              bool
	}{
		{"vendor/foo.go: linguist-vendored: set", "vendor/foo.go", "linguist-vendored", "set", true},
		{"a.go: linguist-generated: unspecified", "a.go", "linguist-generated", "unspecified", true},
		// Path with embedded ": " — git emits this rarely (quoted), but
		// our right-most splitter handles it correctly.
		{"weird:path.go: linguist-vendored: set", "weird:path.go", "linguist-vendored", "set", true},
		{"malformed", "", "", "", false},
		{"only:one separator", "", "", "", false},
	}
	for _, c := range cases {
		p, a, v, ok := splitCheckAttr(c.line)
		if ok != c.ok || p != c.path || a != c.attr || v != c.val {
			t.Errorf("splitCheckAttr(%q) = (%q, %q, %q, %v), want (%q, %q, %q, %v)",
				c.line, p, a, v, ok, c.path, c.attr, c.val, c.ok)
		}
	}
}

func TestLoadSeed_FiltersVendoredAndGenerated(t *testing.T) {
	fr := &stdinFakeRunner{
		fakeRunner: fakeRunner{responses: map[string]string{
			// commitCount
			"rev-list --first-parent --count -n 100 main": "100\n",
			// oldest commit hash
			"rev-list --first-parent --skip 99 -n 1 main": "deadbeef\n",
			// ls-tree output
			"ls-tree -r --name-only deadbeef^": "src/main.go\nvendor/lib.go\npackage-lock.json\nREADME.md\n",
			// check-attr response
			"check-attr --stdin linguist-vendored linguist-generated linguist-documentation": strings.Join([]string{
				"src/main.go: linguist-vendored: unspecified",
				"src/main.go: linguist-generated: unspecified",
				"src/main.go: linguist-documentation: unspecified",
				"vendor/lib.go: linguist-vendored: set",
				"vendor/lib.go: linguist-generated: unspecified",
				"vendor/lib.go: linguist-documentation: unspecified",
				"package-lock.json: linguist-vendored: unspecified",
				"package-lock.json: linguist-generated: set",
				"package-lock.json: linguist-documentation: unspecified",
				"README.md: linguist-vendored: unspecified",
				"README.md: linguist-generated: unspecified",
				"README.md: linguist-documentation: set",
			}, "\n") + "\n",
		}},
	}
	l := NewLoaderWithRunner(fr)
	res, err := l.LoadSeed(LoadRequest{Branch: "main", MaxN: 100}, SeedOptions{})
	if err != nil {
		t.Fatalf("LoadSeed: %v", err)
	}
	got := strings.Join(res.Paths, ",")
	want := "src/main.go,README.md"
	if got != want {
		t.Errorf("paths = %q, want %q", got, want)
	}
	if res.SkippedVendored != 1 {
		t.Errorf("SkippedVendored = %d, want 1", res.SkippedVendored)
	}
	if res.SkippedGenerated != 1 {
		t.Errorf("SkippedGenerated = %d, want 1", res.SkippedGenerated)
	}
	// Documentation default behavior is "kept", so 0 skipped.
	if res.SkippedDocumentation != 0 {
		t.Errorf("SkippedDocumentation = %d, want 0", res.SkippedDocumentation)
	}
}

func TestLoadSeed_IncludeFlagsRestoreFiltered(t *testing.T) {
	fr := &stdinFakeRunner{
		fakeRunner: fakeRunner{responses: map[string]string{
			"rev-list --first-parent --count -n 100 main": "100\n",
			"rev-list --first-parent --skip 99 -n 1 main": "deadbeef\n",
			"ls-tree -r --name-only deadbeef^":            "vendor/lib.go\npackage-lock.json\n",
			"check-attr --stdin linguist-vendored linguist-generated linguist-documentation": strings.Join([]string{
				"vendor/lib.go: linguist-vendored: set",
				"vendor/lib.go: linguist-generated: unspecified",
				"vendor/lib.go: linguist-documentation: unspecified",
				"package-lock.json: linguist-vendored: unspecified",
				"package-lock.json: linguist-generated: set",
				"package-lock.json: linguist-documentation: unspecified",
			}, "\n") + "\n",
		}},
	}
	l := NewLoaderWithRunner(fr)
	res, err := l.LoadSeed(LoadRequest{Branch: "main", MaxN: 100}, SeedOptions{
		IncludeVendored:  true,
		IncludeGenerated: true,
	})
	if err != nil {
		t.Fatalf("LoadSeed: %v", err)
	}
	if len(res.Paths) != 2 {
		t.Errorf("with --show-vendored --show-generated, want 2 paths, got %d: %v", len(res.Paths), res.Paths)
	}
}

func TestLoadSeed_EmptyHistoryReturnsEmpty(t *testing.T) {
	fr := &stdinFakeRunner{
		fakeRunner: fakeRunner{responses: map[string]string{
			"rev-list --first-parent --count main": "0\n",
		}},
	}
	l := NewLoaderWithRunner(fr)
	res, err := l.LoadSeed(LoadRequest{Branch: "main", MaxN: 0}, SeedOptions{})
	if err != nil {
		t.Fatalf("LoadSeed: %v", err)
	}
	if len(res.Paths) != 0 {
		t.Errorf("empty history should yield no seed paths, got %d", len(res.Paths))
	}
}
