// Package gitlog loads commit history from a git repository by
// shelling out to the git CLI. We rely on the CLI rather than a Go-native
// library because git's first-parent / merge-base / patch generation
// behavior is the source of truth, and matching it exactly in another
// implementation is error-prone.
package gitlog

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/S-Nakamur-a/gitfilm/internal/model"
)

type Loader struct {
	repoPath string
	runner   Runner
	diag     io.Writer // optional; if non-nil, per-stage progress is written here
}

// SetDiag enables progress logging for this loader. Lines are formatted
// for direct stderr consumption (each line prefixed with [gitfilm]).
func (l *Loader) SetDiag(w io.Writer) { l.diag = w }

func (l *Loader) logf(format string, args ...interface{}) {
	if l.diag == nil {
		return
	}
	fmt.Fprintf(l.diag, "[gitfilm] "+format+"\n", args...)
}

// Runner is the seam used to shell out to git. Tests replace it with a fake.
type Runner interface {
	Run(args ...string) ([]byte, error)
}

// NewLoader constructs a Loader using the real git binary in the given repo.
func NewLoader(repoPath string) *Loader {
	return &Loader{repoPath: repoPath, runner: &execRunner{repoPath: repoPath}}
}

// NewLoaderWithRunner is for tests.
func NewLoaderWithRunner(r Runner) *Loader {
	return &Loader{runner: r}
}

type execRunner struct{ repoPath string }

func (e *execRunner) Run(args ...string) ([]byte, error) {
	cmd := exec.Command("git", append([]string{"-C", e.repoPath}, args...)...)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return out.Bytes(), nil
}

type LoadRequest struct {
	Branch  string
	Against string
	SubDir  string
	MaxN    int
}

// Load walks Branch with --first-parent and produces a History where each
// commit is tagged Feature (if it is on Branch but not on Against) or
// Against (otherwise). Commits are returned oldest-first.
//
// Internally we use a SINGLE `git log -p` invocation rather than one
// `git show` per commit. On a 7900-commit repo that's the difference
// between an 80s freeze and a 1-2s load, since we pay subprocess fork
// once instead of N times.
func (l *Loader) Load(req LoadRequest) (model.History, error) {
	if req.Branch == "" {
		return model.History{}, fmt.Errorf("branch is required")
	}
	featureSet, err := l.featureSet(req.Branch, req.Against)
	if err != nil {
		featureSet = nil // missing against branch -> tag everything Feature
	}

	commits, err := l.loadCommitsBatched(req)
	if err != nil {
		return model.History{}, err
	}
	for i := range commits {
		if featureSet == nil {
			commits[i].Tag = model.BranchTagFeature
		} else if _, ok := featureSet[commits[i].Hash]; ok {
			commits[i].Tag = model.BranchTagFeature
		} else {
			commits[i].Tag = model.BranchTagAgainst
		}
	}
	// reverse to oldest-first
	for i, j := 0, len(commits)-1; i < j; i, j = i+1, j-1 {
		commits[i], commits[j] = commits[j], commits[i]
	}
	return model.History{
		Branch:  req.Branch,
		Against: req.Against,
		SubDir:  req.SubDir,
		Commits: commits,
	}, nil
}

// loadCommitsBatched runs `git log -p` and parses the streamed output
// into a slice of Commits (newest-first).
//
// For large histories we shard the work across N workers, each running
// its own `git log --skip M -n K -p` over a slice of the commit range.
// `git log -p`'s wall time is dominated by per-commit diff generation
// inside git itself (CPU bound), so launching multiple git processes
// scales nearly linearly up to the core count.
func (l *Loader) loadCommitsBatched(req LoadRequest) ([]model.Commit, error) {
	tCount := time.Now()
	total, err := l.commitCount(req)
	if err != nil {
		return nil, err
	}
	l.logf("rev-list count: %d commits in %s", total, time.Since(tCount).Round(time.Millisecond))
	if total == 0 {
		return nil, nil
	}
	// Shard size tuned so each chunk does enough work to amortize the
	// fork/exec cost (~10ms) but not so much that one slow shard holds
	// up everything. ~1000 commits/shard hits a good balance on
	// monorepos in informal testing.
	const targetShard = 1000
	workers := runtime.NumCPU()
	if workers > 8 {
		workers = 8 // diminishing returns past 8 due to disk contention
	}
	shards := (total + targetShard - 1) / targetShard
	if shards < workers {
		workers = shards
	}
	if workers < 1 {
		workers = 1
	}
	shardSize := (total + shards - 1) / shards
	l.logf("sharding: %d shards × %d commits, %d workers", shards, shardSize, workers)

	type result struct {
		idx      int
		commits  []model.Commit
		gitDur   time.Duration
		parseDur time.Duration
		bytes    int
		err      error
	}
	results := make(chan result, shards)
	jobs := make(chan int, shards)
	var done int64
	tShardStart := time.Now()
	for w := 0; w < workers; w++ {
		go func() {
			for shardIdx := range jobs {
				skip := shardIdx * shardSize
				take := shardSize
				if skip+take > total {
					take = total - skip
				}
				cs, gitDur, parseDur, nBytes, err := l.loadShardTimed(req, skip, take)
				if l.diag != nil {
					n := atomic.AddInt64(&done, 1)
					l.logf("shard %d/%d done: skip=%d take=%d git=%s parse=%s bytes=%.1f MB (%d/%d shards, %s elapsed)",
						shardIdx, shards-1, skip, take,
						gitDur.Round(time.Millisecond), parseDur.Round(time.Millisecond),
						float64(nBytes)/1024/1024, n, int64(shards),
						time.Since(tShardStart).Round(time.Millisecond))
				}
				results <- result{idx: shardIdx, commits: cs, gitDur: gitDur, parseDur: parseDur, bytes: nBytes, err: err}
			}
		}()
	}
	for i := 0; i < shards; i++ {
		jobs <- i
	}
	close(jobs)

	collected := make([][]model.Commit, shards)
	var totalGit, totalParse time.Duration
	var totalBytes int
	for i := 0; i < shards; i++ {
		r := <-results
		if r.err != nil {
			return nil, r.err
		}
		collected[r.idx] = r.commits
		totalGit += r.gitDur
		totalParse += r.parseDur
		totalBytes += r.bytes
	}
	l.logf("shards finished: wall=%s, summed git=%s, summed parse=%s, total bytes=%.1f MB",
		time.Since(tShardStart).Round(time.Millisecond),
		totalGit.Round(time.Millisecond),
		totalParse.Round(time.Millisecond),
		float64(totalBytes)/1024/1024)

	// Stitch shards in order (shard 0 = newest).
	out := make([]model.Commit, 0, total)
	for _, cs := range collected {
		out = append(out, cs...)
	}
	return out, nil
}

// commitCount returns how many commits we'd load for this request,
// using `git rev-list --count` (much cheaper than -p).
func (l *Loader) commitCount(req LoadRequest) (int, error) {
	args := []string{"rev-list", "--first-parent", "--count"}
	if req.MaxN > 0 {
		args = append(args, "-n", strconv.Itoa(req.MaxN))
	}
	args = append(args, req.Branch)
	if req.SubDir != "" {
		args = append(args, "--", req.SubDir)
	}
	out, err := l.runner.Run(args...)
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return 0, fmt.Errorf("rev-list --count: %q: %w", string(out), err)
	}
	if req.MaxN > 0 && n > req.MaxN {
		n = req.MaxN
	}
	return n, nil
}

// loadShardTimed runs git log -p for a window [skip, skip+take) of
// the first-parent history, parses the output, and surfaces per-stage
// timings and byte counts for diagnostics. Returned durations are wall
// time inside `git log` (subprocess) and the local diff parser.
func (l *Loader) loadShardTimed(req LoadRequest, skip, take int) ([]model.Commit, time.Duration, time.Duration, int, error) {
	const format = beginMarker + "%n%H%n%h%n%an%n%ae%n%at%n%s%n" +
		bodyOpenMarker + "%n%b%n" + bodyCloseMarker
	args := []string{
		"log", "--first-parent", "--no-color", "-M",
		"--unified=1", "-p",
		"--format=" + format,
		"--skip", strconv.Itoa(skip),
		"-n", strconv.Itoa(take),
	}
	args = append(args, req.Branch)
	if req.SubDir != "" {
		args = append(args, "--", req.SubDir)
	}
	tGit := time.Now()
	out, err := l.runner.Run(args...)
	gitDur := time.Since(tGit)
	if err != nil {
		return nil, gitDur, 0, 0, err
	}
	tParse := time.Now()
	cs, err := parseLogP(out, req.SubDir)
	parseDur := time.Since(tParse)
	return cs, gitDur, parseDur, len(out), err
}

// featureSet returns the set of commits reachable from branch but not
// from against (the classic "branch ^against" range).
func (l *Loader) featureSet(branch, against string) (map[string]struct{}, error) {
	out, err := l.runner.Run("rev-list", "--first-parent", branch, "^"+against)
	if err != nil {
		return nil, err
	}
	set := make(map[string]struct{})
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		if h := strings.TrimSpace(sc.Text()); h != "" {
			set[h] = struct{}{}
		}
	}
	return set, sc.Err()
}

// Marker lines used to bracket the commit metadata in `git log -p`
// output. They are unlikely to appear in real subjects, bodies, or
// diff content; if they ever do, we'd misparse a single commit but
// not crash.
const (
	beginMarker     = "__GITFILM_BEGIN__"
	bodyOpenMarker  = "__GITFILM_BODY__"
	bodyCloseMarker = "__GITFILM_END__"
)

// parseLogP scans the streamed output of `git log -p --format=...`
// using the marker-line format defined in loadCommitsBatched.
//
// Each commit chunk looks like:
//
//	__GITFILM_BEGIN__
//	<hash>
//	<short>
//	<author name>
//	<author email>
//	<unix time>
//	<subject>
//	__GITFILM_BODY__
//	<body line 1>
//	<body line 2>
//	__GITFILM_END__
//	<diff lines for this commit, until the next BEGIN marker>
func parseLogP(b []byte, subdir string) ([]model.Commit, error) {
	var commits []model.Commit
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 0, 64*1024), 64*1024*1024)

	type state int
	const (
		sBetween state = iota
		sHeaders
		sBody
		sDiff
	)
	st := sBetween

	var (
		cur     model.Commit
		bodyBuf strings.Builder
		diffBuf bytes.Buffer
		hdrIdx  int // 0..5 -> hash,short,name,email,time,subject
	)

	finishCommit := func() error {
		if cur.Hash == "" {
			return nil
		}
		cur.Body = strings.TrimRight(bodyBuf.String(), "\n")
		files, err := parseFileDiffs(diffBuf.Bytes(), subdir)
		if err != nil {
			return fmt.Errorf("parse diffs for %s: %w", cur.Hash, err)
		}
		cur.Files = files
		commits = append(commits, cur)
		cur = model.Commit{}
		bodyBuf.Reset()
		diffBuf.Reset()
		hdrIdx = 0
		return nil
	}

	for sc.Scan() {
		line := sc.Text()
		if line == beginMarker {
			if err := finishCommit(); err != nil {
				return nil, err
			}
			st = sHeaders
			continue
		}
		switch st {
		case sBetween:
			// pre-amble between log start and first commit; ignore
		case sHeaders:
			if line == bodyOpenMarker {
				st = sBody
				continue
			}
			switch hdrIdx {
			case 0:
				cur.Hash = line
			case 1:
				cur.ShortHash = line
			case 2:
				cur.AuthorName = line
			case 3:
				cur.Author = fmt.Sprintf("%s <%s>", cur.AuthorName, line)
			case 4:
				when, _ := strconv.ParseInt(strings.TrimSpace(line), 10, 64)
				cur.When = unixToTime(when)
			case 5:
				cur.Subject = line
			}
			hdrIdx++
		case sBody:
			if line == bodyCloseMarker {
				st = sDiff
				continue
			}
			bodyBuf.WriteString(line)
			bodyBuf.WriteByte('\n')
		case sDiff:
			diffBuf.WriteString(line)
			diffBuf.WriteByte('\n')
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if err := finishCommit(); err != nil {
		return nil, err
	}
	return commits, nil
}
