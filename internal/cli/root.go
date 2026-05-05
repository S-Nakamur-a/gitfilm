package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/S-Nakamur-a/gitfilm/internal/gitlog"
	"github.com/S-Nakamur-a/gitfilm/internal/model"
	"github.com/S-Nakamur-a/gitfilm/internal/output"
	"github.com/S-Nakamur-a/gitfilm/internal/replay"
	"github.com/spf13/cobra"
)

type options struct {
	against string
	mode    string
	repo    string
	subdir  string
	htmlOut string
	maxN    int
	stats   bool
	verbose bool
}

func New() *cobra.Command {
	var opts options
	cmd := &cobra.Command{
		Use:   "git-film <branch>",
		Short: "Replay your git history as an animation",
		Long: "git-film walks a branch with --first-parent, tags commits by their\n" +
			"originating branch (split at merge-base with --against), and replays\n" +
			"the diffs as an animation in the terminal or a single HTML file.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(args[0], opts)
		},
	}
	cmd.Flags().StringVar(&opts.against, "against", "main", "branch considered the parent (merge-base split point)")
	cmd.Flags().StringVarP(&opts.mode, "output", "o", "tui", "output mode: "+strings.Join(output.Names(), " | "))
	cmd.Flags().StringVar(&opts.repo, "repo", ".", "path to the git repository")
	cmd.Flags().StringVar(&opts.subdir, "path", "", "limit to changes under this subdirectory (relative to repo root)")
	cmd.Flags().StringVar(&opts.htmlOut, "html-out", "gitfilm.html", "html output file path (when -o html)")
	cmd.Flags().IntVar(&opts.maxN, "max", 500, "limit to the most recent N commits (0 = no limit, careful on big repos)")
	cmd.Flags().BoolVar(&opts.stats, "stats", false, "print load time, dwell distribution, and per-commit stats; do not render")
	cmd.Flags().BoolVarP(&opts.verbose, "verbose", "v", false, "log per-stage timings and memory to stderr (always on for non-tui modes)")
	return cmd
}

func run(branch string, opts options) error {
	repo, err := filepath.Abs(opts.repo)
	if err != nil {
		return fmt.Errorf("resolve repo path: %w", err)
	}
	loader := gitlog.NewLoader(repo)
	verbose := opts.verbose || opts.mode != "tui"
	if verbose {
		loader.SetDiag(os.Stderr)
		fmt.Fprintf(os.Stderr, "[gitfilm] starting: branch=%s against=%s mode=%s max=%d subdir=%q\n",
			branch, opts.against, opts.mode, opts.maxN, opts.subdir)
	}

	loadStart := time.Now()
	frames, err := loader.Load(gitlog.LoadRequest{
		Branch:  branch,
		Against: opts.against,
		SubDir:  opts.subdir,
		MaxN:    opts.maxN,
	})
	loadDur := time.Since(loadStart)
	if err != nil {
		return fmt.Errorf("load history: %w", err)
	}
	if len(frames.Commits) == 0 {
		return errors.New("no commits found for the given branch")
	}
	if verbose {
		var mem runtime.MemStats
		runtime.ReadMemStats(&mem)
		var files, hunks, lines int
		for _, c := range frames.Commits {
			files += len(c.Files)
			for _, f := range c.Files {
				hunks += len(f.Hunks)
				for _, h := range f.Hunks {
					lines += len(h.Lines)
				}
			}
		}
		fmt.Fprintf(os.Stderr,
			"[gitfilm] loaded: %s — %d commits, %d files, %d hunks, %d diff-lines, alloc=%.1f MB\n",
			loadDur.Round(time.Millisecond), len(frames.Commits), files, hunks, lines,
			float64(mem.Alloc)/1024/1024)
	}

	if opts.stats {
		printStats(os.Stderr, frames, loadDur)
		return nil
	}

	r, ok := output.Get(opts.mode)
	if !ok {
		return fmt.Errorf("unknown output mode %q (want one of: %s)", opts.mode, strings.Join(output.Names(), ", "))
	}
	cfg := output.Config{HTMLOutPath: opts.htmlOut}
	renderStart := time.Now()
	if err := r.Run(frames, cfg, os.Stderr); err != nil {
		return err
	}
	if verbose {
		var mem runtime.MemStats
		runtime.ReadMemStats(&mem)
		fmt.Fprintf(os.Stderr,
			"[gitfilm] render done: %s (alloc=%.1f MB sys=%.1f MB)\n",
			time.Since(renderStart).Round(time.Millisecond),
			float64(mem.Alloc)/1024/1024, float64(mem.Sys)/1024/1024)
	}
	return nil
}

// printStats verifies the perf wins from the recent tuning by
// computing per-commit dwell times the way the TUI would, and dumps a
// distribution so we can see whether single-file commits really feel
// snappy and whether 10K-commit loads stay under control.
func printStats(w *os.File, h model.History, loadDur time.Duration) {
	var (
		mem        runtime.MemStats
		dwells     = make([]time.Duration, 0, len(h.Commits))
		singleDw   []time.Duration
		multiDw    []time.Duration
		fileCounts = make(map[int]int)
		filesTotal int
		hunksTotal int
		linesTotal int
	)
	runtime.ReadMemStats(&mem)
	for _, c := range h.Commits {
		d := replay.DwellFor(c)
		dwells = append(dwells, d)
		if len(c.Files) <= 1 {
			singleDw = append(singleDw, d)
		} else {
			multiDw = append(multiDw, d)
		}
		fileCounts[len(c.Files)]++
		filesTotal += len(c.Files)
		for _, f := range c.Files {
			hunksTotal += len(f.Hunks)
			for _, hk := range f.Hunks {
				linesTotal += len(hk.Lines)
			}
		}
	}

	fmt.Fprintf(w, "=== gitfilm stats: %s ⇒ %s ===\n", h.Branch, h.Against)
	fmt.Fprintf(w, "load time:        %s\n", loadDur.Round(time.Millisecond))
	fmt.Fprintf(w, "commits:          %d\n", len(h.Commits))
	fmt.Fprintf(w, "files (sum):      %d  (avg %.1f / commit)\n", filesTotal, float64(filesTotal)/float64(len(h.Commits)))
	fmt.Fprintf(w, "hunks (sum):      %d\n", hunksTotal)
	fmt.Fprintf(w, "diff lines kept:  %d\n", linesTotal)
	fmt.Fprintf(w, "alloc resident:   %.1f MB  (sys %.1f MB)\n",
		float64(mem.Alloc)/1024/1024, float64(mem.Sys)/1024/1024)

	fmt.Fprintf(w, "\n--- dwell distribution (all %d commits) ---\n", len(dwells))
	printPercentiles(w, dwells)
	fmt.Fprintf(w, "\n--- dwell distribution (1-file commits, %d) ---\n", len(singleDw))
	printPercentiles(w, singleDw)
	fmt.Fprintf(w, "\n--- dwell distribution (multi-file commits, %d) ---\n", len(multiDw))
	printPercentiles(w, multiDw)

	fmt.Fprintf(w, "\n--- file-count buckets ---\n")
	keys := make([]int, 0, len(fileCounts))
	for k := range fileCounts {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	for _, k := range keys {
		bar := ""
		if cnt := fileCounts[k]; cnt > 0 {
			n := cnt * 40 / len(h.Commits)
			if n == 0 && cnt > 0 {
				n = 1
			}
			for i := 0; i < n; i++ {
				bar += "█"
			}
		}
		fmt.Fprintf(w, "  %3d files: %5d commits  %s\n", k, fileCounts[k], bar)
	}

	totalPlay := time.Duration(0)
	for _, d := range dwells {
		totalPlay += d
	}
	fmt.Fprintf(w, "\nestimated total play time at 1.0x: %s\n", totalPlay.Round(time.Second))
}

func printPercentiles(w *os.File, ds []time.Duration) {
	if len(ds) == 0 {
		fmt.Fprintln(w, "  (none)")
		return
	}
	sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
	pct := func(p float64) time.Duration {
		i := int(float64(len(ds)-1) * p)
		return ds[i]
	}
	fmt.Fprintf(w, "  min %s   p50 %s   p90 %s   p99 %s   max %s\n",
		ds[0].Round(time.Millisecond),
		pct(0.5).Round(time.Millisecond),
		pct(0.9).Round(time.Millisecond),
		pct(0.99).Round(time.Millisecond),
		ds[len(ds)-1].Round(time.Millisecond))
}
