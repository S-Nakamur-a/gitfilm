package gitlog

import (
	"bufio"
	"bytes"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// SeedResult is the listing of files that already lived in the
// working tree at the parent of the oldest loaded commit. Used to
// pre-populate TreeState's "existing" set so the live filesystem
// view shows surrounding context even when --max truncates history.
//
// Paths are forward-slash separated, relative to the repo root (or
// to SubDir when set on the request). Filtering of vendored /
// generated / documentation artifacts has already been applied —
// callers receive a payload that's safe to seed directly.
//
// Empty result + nil error is the expected outcome when there is no
// parent (i.e. the loaded window already covers the repo's first
// commit) or when the user disabled seeding via --seed-tree=off.
type SeedResult struct {
	// Paths is the post-filter file list, suitable for TreeState.Seed.
	Paths []string

	// Skipped tallies how many entries each linguist filter dropped.
	// Surfaced via diag logs so users can confirm vendor/ et al. were
	// correctly excluded from the seed.
	SkippedVendored      int
	SkippedGenerated     int
	SkippedDocumentation int
}

// SeedOptions controls which linguist-* attribute classes are
// excluded from the seed. The defaults reflect "what GitHub folds in
// PR diffs": vendored and generated paths are filtered (they bloat
// the seed without adding signal), documentation is kept.
type SeedOptions struct {
	IncludeVendored      bool // when true, do not filter linguist-vendored
	IncludeGenerated     bool // when true, do not filter linguist-generated
	IncludeDocumentation bool // (always kept; reserved for future use)
}

// LoadSeed enumerates the working-tree files at the commit immediately
// preceding the oldest commit that Load / LoadStream would surface for
// req, then drops linguist-vendored / linguist-generated entries (per
// opts). Results are sorted lexicographically.
//
// The cost is two cheap git invocations regardless of repo size:
//
//  1. `git rev-list --first-parent --skip <total-1> -n 1 <branch>` to
//     identify the oldest loaded commit's hash.
//  2. `git ls-tree -r --name-only <hash>^` to list its parent's
//     working tree.
//  3. `git check-attr ... --stdin` (one fork/exec total, paths piped)
//     to classify the listing.
//
// All filtering is delegated to git itself so we don't reimplement
// .gitattributes parsing.
//
// If req.MaxN is 0 (no truncation) or the oldest commit has no parent
// (the repo's root commit is the oldest), LoadSeed returns an empty
// SeedResult — there is no "before the window" to describe.
func (l *Loader) LoadSeed(req LoadRequest, opts SeedOptions) (SeedResult, error) {
	if req.Branch == "" {
		return SeedResult{}, fmt.Errorf("branch is required")
	}

	tStart := time.Now()
	total, err := l.commitCount(req)
	if err != nil {
		return SeedResult{}, err
	}
	if total == 0 {
		return SeedResult{}, nil
	}

	// Determine the oldest loaded commit. The loader walks
	// --first-parent newest-first with `git log -p --skip M -n K`,
	// so commit at skip=total-1 is the oldest.
	args := []string{"rev-list", "--first-parent", "--skip", strconv.Itoa(total - 1), "-n", "1", req.Branch}
	out, err := l.runner.Run(args...)
	if err != nil {
		return SeedResult{}, fmt.Errorf("seed: locate oldest commit: %w", err)
	}
	oldest := strings.TrimSpace(string(out))
	if oldest == "" {
		return SeedResult{}, nil
	}

	// `<rev>^` is the parent. If oldest is the root commit, this
	// resolves to a missing reference and ls-tree returns an error;
	// degrade to "no seed" in that case rather than aborting the load.
	parentRev := oldest + "^"
	lsArgs := []string{"ls-tree", "-r", "--name-only", parentRev}
	if req.SubDir != "" {
		lsArgs = append(lsArgs, "--", req.SubDir)
	}
	tree, err := l.runner.Run(lsArgs...)
	if err != nil {
		l.logf("seed: ls-tree %s failed (likely root commit): %v", parentRev, err)
		return SeedResult{}, nil
	}

	allPaths := splitNullableLines(tree)
	if len(allPaths) == 0 {
		return SeedResult{}, nil
	}
	l.logf("seed: ls-tree %s -> %d paths in %s", parentRev, len(allPaths), time.Since(tStart).Round(time.Millisecond))

	tAttr := time.Now()
	classes, err := l.checkAttr(allPaths)
	if err != nil {
		// Attribute lookup failure is non-fatal: fall back to "all
		// paths kept". Better to over-seed than abort the load.
		l.logf("seed: check-attr failed (%v); using unfiltered seed", err)
		return SeedResult{Paths: allPaths}, nil
	}
	l.logf("seed: check-attr %d paths in %s", len(allPaths), time.Since(tAttr).Round(time.Millisecond))

	res := SeedResult{Paths: make([]string, 0, len(allPaths))}
	for _, p := range allPaths {
		c := classes[p]
		switch {
		case c.Vendored && !opts.IncludeVendored:
			res.SkippedVendored++
		case c.Generated && !opts.IncludeGenerated:
			res.SkippedGenerated++
		case c.Documentation && !opts.IncludeDocumentation:
			// Documentation is currently always kept; the field is
			// here so a future flag can flip the default.
			res.Paths = append(res.Paths, p)
		default:
			res.Paths = append(res.Paths, p)
		}
	}
	if res.SkippedVendored+res.SkippedGenerated+res.SkippedDocumentation > 0 {
		l.logf("seed: filtered vendored=%d generated=%d documentation=%d (kept %d)",
			res.SkippedVendored, res.SkippedGenerated, res.SkippedDocumentation, len(res.Paths))
	}
	return res, nil
}

// linguistClass collects the three GitHub-relevant attribute results
// for one path. Each field is true when `git check-attr` reported the
// attribute as `set` (it ignores `unspecified` and `unset`).
type linguistClass struct {
	Vendored      bool
	Generated     bool
	Documentation bool
}

// checkAttr asks git which paths carry which linguist-* attributes.
// One subprocess invocation handles the whole list via --stdin.
func (l *Loader) checkAttr(paths []string) (map[string]linguistClass, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	var stdin bytes.Buffer
	for _, p := range paths {
		stdin.WriteString(p)
		stdin.WriteByte('\n')
	}
	args := []string{
		"check-attr", "--stdin",
		"linguist-vendored", "linguist-generated", "linguist-documentation",
	}
	out, err := l.runner.RunStdin(stdin.Bytes(), args...)
	if err != nil {
		return nil, err
	}
	classes := make(map[string]linguistClass, len(paths))
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		// Output format: "<path>: <attr>: <set|unset|unspecified>".
		// Path may itself contain ": " when it has spaces, but git's
		// emitter quotes those — we split from the right via the last
		// two ": " separators.
		path, attr, value, ok := splitCheckAttr(line)
		if !ok || value != "set" {
			continue
		}
		c := classes[path]
		switch attr {
		case "linguist-vendored":
			c.Vendored = true
		case "linguist-generated":
			c.Generated = true
		case "linguist-documentation":
			c.Documentation = true
		}
		classes[path] = c
	}
	return classes, sc.Err()
}

// splitCheckAttr parses one line of `git check-attr --stdin` output
// into (path, attr, value). The format is "<path>: <attr>: <value>"
// where <path> may itself contain ":" (and even ": "). We split
// rightward from the last two ": " separators.
//
// Returns ok=false if the line does not match the expected shape.
func splitCheckAttr(line string) (path, attr, value string, ok bool) {
	i := strings.LastIndex(line, ": ")
	if i < 0 {
		return "", "", "", false
	}
	value = line[i+2:]
	rest := line[:i]
	j := strings.LastIndex(rest, ": ")
	if j < 0 {
		return "", "", "", false
	}
	attr = rest[j+2:]
	path = rest[:j]
	return path, attr, value, true
}

// splitNullableLines splits ls-tree output by newline. ls-tree without
// `-z` emits LF-separated lines; we keep that mode because we never
// pass `-z`. Trailing empty lines are dropped.
func splitNullableLines(b []byte) []string {
	out := make([]string, 0, 256)
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			continue
		}
		out = append(out, line)
	}
	return out
}
