// Package stats prints the --stats report: dwell distributions,
// per-commit breakdowns, and total play-time estimates. The output is
// the measurement harness the codebase was tuned against, so the
// formatting is part of the user-visible contract — keep it stable.
package stats

import (
	"fmt"
	"io"
	"runtime"
	"sort"
	"time"

	"github.com/S-Nakamur-a/gitfilm/internal/model"
	"github.com/S-Nakamur-a/gitfilm/internal/replay"
)

// Print writes a full --stats report to w, comparing single-file vs.
// multi-file commits, file-count buckets, and total estimated play
// time at 1.0× speed. loadDur is the wall-clock time spent in
// gitlog.Loader.Load (so users can attribute slowness to load vs.
// render).
func Print(w io.Writer, h model.History, loadDur time.Duration) {
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
			if n == 0 {
				n = 1
			}
			for range n {
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

func printPercentiles(w io.Writer, ds []time.Duration) {
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
