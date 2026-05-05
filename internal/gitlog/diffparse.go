package gitlog

import (
	"bufio"
	"bytes"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/S-Nakamur-a/gitfilm/internal/model"
)

func unixToTime(unix int64) time.Time { return time.Unix(unix, 0) }

// Per-commit content caps. The TUI/HTML can only show a handful of
// files and a handful of lines per hunk, so keeping thousands of
// either burns memory without changing what the user sees. These are
// soft caps applied at parse time.
const (
	maxLinesPerHunk = 80
	maxHunksPerFile = 30
	maxFilesPerCommit = 100
)

// parseFileDiffs reads the body that follows `git show --format=...`,
// which is unified-diff text covering all files in the commit. We parse
// out per-file FileChange entries with their hunks.
//
// We do NOT use --numstat here on purpose: we want the full hunk text
// for the typing animation. Added/Removed counts are derived from the
// hunk lines.
//
// If subdir is non-empty, only files whose path is under subdir are kept.
func parseFileDiffs(b []byte, subdir string) ([]model.FileChange, error) {
	var files []model.FileChange
	var cur *model.FileChange
	var curHunk *model.Hunk

	flushHunk := func() {
		if cur != nil && curHunk != nil {
			if len(cur.Hunks) < maxHunksPerFile {
				cur.Hunks = append(cur.Hunks, *curHunk)
			}
			curHunk = nil
		}
	}
	flushFile := func() {
		flushHunk()
		if cur != nil {
			if pathUnder(cur.Path, subdir) || pathUnder(cur.OldPath, subdir) {
				if len(files) < maxFilesPerCommit {
					files = append(files, *cur)
				}
			}
			cur = nil
		}
	}

	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "diff --git "):
			flushFile()
			cur = &model.FileChange{Status: model.StatusModified}
			a, bp := parseDiffGitPaths(line)
			cur.OldPath = a
			cur.Path = bp
		case cur == nil:
			// header noise (e.g. blank line before the first diff)
			continue
		case strings.HasPrefix(line, "new file mode"):
			cur.Status = model.StatusAdded
			cur.OldPath = ""
		case strings.HasPrefix(line, "deleted file mode"):
			cur.Status = model.StatusDeleted
			cur.Path = cur.OldPath
		case strings.HasPrefix(line, "rename from "):
			cur.Status = model.StatusRenamed
			cur.OldPath = strings.TrimPrefix(line, "rename from ")
		case strings.HasPrefix(line, "rename to "):
			cur.Path = strings.TrimPrefix(line, "rename to ")
		case strings.HasPrefix(line, "copy from "):
			cur.Status = model.StatusCopied
			cur.OldPath = strings.TrimPrefix(line, "copy from ")
		case strings.HasPrefix(line, "copy to "):
			cur.Path = strings.TrimPrefix(line, "copy to ")
		case strings.HasPrefix(line, "--- "), strings.HasPrefix(line, "+++ "),
			strings.HasPrefix(line, "index "), strings.HasPrefix(line, "similarity "),
			strings.HasPrefix(line, "Binary files "):
			// metadata we don't need
		case strings.HasPrefix(line, "@@"):
			flushHunk()
			h, err := parseHunkHeader(line)
			if err != nil {
				return nil, err
			}
			curHunk = &h
		case curHunk != nil && len(line) > 0:
			// Always count adds/removes for the file totals so the
			// header stays accurate, but stop accumulating Lines past
			// the cap to keep memory bounded.
			capped := len(curHunk.Lines) >= maxLinesPerHunk
			switch line[0] {
			case '+':
				if !capped {
					curHunk.Lines = append(curHunk.Lines, model.DiffLine{Kind: model.LineAdded, Text: line[1:]})
				}
				cur.Added++
			case '-':
				if !capped {
					curHunk.Lines = append(curHunk.Lines, model.DiffLine{Kind: model.LineRemoved, Text: line[1:]})
				}
				cur.Removed++
			case ' ':
				if !capped {
					curHunk.Lines = append(curHunk.Lines, model.DiffLine{Kind: model.LineContext, Text: line[1:]})
				}
			case '\\':
				// "\ No newline at end of file" — ignore
			}
		}
	}
	flushFile()
	return files, sc.Err()
}

// parseDiffGitPaths extracts a and b paths from a `diff --git a/foo b/bar` line.
// It tolerates spaces in paths by scanning from the end.
func parseDiffGitPaths(line string) (a, b string) {
	const prefix = "diff --git "
	rest := strings.TrimPrefix(line, prefix)
	// rest := "a/foo b/bar" — but either side may contain spaces; for our
	// purposes (real-world repos rarely have spaces), the simple split is fine.
	if i := strings.Index(rest, " b/"); i > 0 {
		a = strings.TrimPrefix(rest[:i], "a/")
		b = strings.TrimPrefix(rest[i+1:], "b/")
		return
	}
	// fallback
	parts := strings.Fields(rest)
	if len(parts) == 2 {
		a = strings.TrimPrefix(parts[0], "a/")
		b = strings.TrimPrefix(parts[1], "b/")
	}
	return
}

func parseHunkHeader(line string) (model.Hunk, error) {
	// @@ -oldStart,oldLines +newStart,newLines @@ optional context
	// counts default to 1 when omitted ("@@ -1 +1 @@")
	var h model.Hunk
	end := strings.Index(line[2:], "@@")
	if end < 0 {
		return h, fmt.Errorf("bad hunk header: %s", line)
	}
	body := strings.TrimSpace(line[2 : 2+end])
	h.Header = strings.TrimSpace(line[2+end+2:])
	parts := strings.Fields(body)
	if len(parts) != 2 {
		return h, fmt.Errorf("bad hunk header parts: %s", line)
	}
	old := strings.TrimPrefix(parts[0], "-")
	new_ := strings.TrimPrefix(parts[1], "+")
	h.OldStart, h.OldLines = parseRange(old)
	h.NewStart, h.NewLines = parseRange(new_)
	return h, nil
}

func parseRange(s string) (start, count int) {
	count = 1
	if i := strings.IndexByte(s, ','); i >= 0 {
		start, _ = strconv.Atoi(s[:i])
		count, _ = strconv.Atoi(s[i+1:])
	} else {
		start, _ = strconv.Atoi(s)
	}
	return
}

// pathUnder reports whether path is at or below subdir.
// An empty subdir means "no filter": every non-empty path matches.
// An empty path never matches.
func pathUnder(path, subdir string) bool {
	if path == "" {
		return false
	}
	if subdir == "" {
		return true
	}
	subdir = strings.TrimSuffix(subdir, "/")
	return path == subdir || strings.HasPrefix(path, subdir+"/")
}
