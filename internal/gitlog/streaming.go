package gitlog

import (
	"fmt"
	"runtime"
	"sync"
	"time"

	"github.com/S-Nakamur-a/gitfilm/internal/model"
)

// LoadBatch is one chunk of progressively-loaded history. Commits are
// in oldest-first order, ready to be appended to a History.
//
// Total is the eventually-final commit count. It's set on the first
// non-empty batch and remains constant for the rest of the stream.
//
// Done = true on the final batch (whether it carried Commits or Err).
// Err is set when streaming aborted; any commits delivered earlier are
// still valid and can be displayed.
type LoadBatch struct {
	Commits []model.Commit
	Total   int
	Done    bool
	Err     error
}

// LoadStream walks the same first-parent history as Load but emits
// batches into out as shards complete, ordered oldest-first regardless
// of internal completion order. The returned channel is closed when
// streaming finishes (success, error, or empty history).
//
// Why oldest-first: the TUI replays from the beginning, so it needs
// the OLDEST commits first to show anything. Internally git log only
// supports `--skip M -n K` from the newest end, so we shard that way
// and reorder delivery: highest shard index = oldest commits, emit it
// first; lowest shard index = newest commits, emit it last.
//
// The returned channel is buffered (capacity = shards) so a slow
// consumer doesn't block subsequent shard completions.
func (l *Loader) LoadStream(req LoadRequest) <-chan LoadBatch {
	// Capacity sized to whatever sharding will compute, but we don't
	// know that until commitCount runs. Use a reasonable upper bound
	// so producers never block on send.
	out := make(chan LoadBatch, 16)
	go l.loadStream(req, out)
	return out
}

func (l *Loader) loadStream(req LoadRequest, out chan<- LoadBatch) {
	defer close(out)
	if req.Branch == "" {
		out <- LoadBatch{Done: true, Err: fmt.Errorf("branch is required")}
		return
	}

	featureSet, err := l.featureSet(req.Branch, req.Against)
	if err != nil {
		// Same graceful degradation as Load: missing 'against' tags
		// every commit Feature.
		featureSet = nil
	}

	tCount := time.Now()
	total, err := l.commitCount(req)
	if err != nil {
		out <- LoadBatch{Done: true, Err: err}
		return
	}
	l.logf("stream rev-list count: %d commits in %s", total, time.Since(tCount).Round(time.Millisecond))
	if total == 0 {
		out <- LoadBatch{Total: 0, Done: true}
		return
	}

	const targetShard = 1000
	workers := runtime.NumCPU()
	if workers > 8 {
		workers = 8
	}
	shards := (total + targetShard - 1) / targetShard
	if shards < workers {
		workers = shards
	}
	if workers < 1 {
		workers = 1
	}
	shardSize := (total + shards - 1) / shards
	l.logf("stream sharding: %d shards × %d commits, %d workers", shards, shardSize, workers)

	type result struct {
		idx     int
		commits []model.Commit
		err     error
	}
	results := make(chan result, shards)
	jobs := make(chan int, shards)

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for shardIdx := range jobs {
				skip := shardIdx * shardSize
				take := shardSize
				if skip+take > total {
					take = total - skip
				}
				cs, _, _, _, err := l.loadShardTimed(req, skip, take)
				if err == nil {
					// Tag according to feature set, mirroring Load.
					for i := range cs {
						if featureSet == nil {
							cs[i].Tag = model.BranchTagFeature
						} else if _, ok := featureSet[cs[i].Hash]; ok {
							cs[i].Tag = model.BranchTagFeature
						} else {
							cs[i].Tag = model.BranchTagAgainst
						}
					}
					// Reverse so oldest comes first within the shard;
					// the consumer can append straight to its history.
					for i, j := 0, len(cs)-1; i < j; i, j = i+1, j-1 {
						cs[i], cs[j] = cs[j], cs[i]
					}
				}
				results <- result{idx: shardIdx, commits: cs, err: err}
			}
		}()
	}
	for i := 0; i < shards; i++ {
		jobs <- i
	}
	close(jobs)

	// Order delivery: highest shard idx (oldest commits) first. Buffer
	// out-of-order arrivals in pending until the next-needed shard
	// shows up. If any shard fails, stop emitting further batches and
	// signal the error on the final batch — partial deliveries before
	// the failure are kept (the consumer already saw them).
	pending := make(map[int][]model.Commit)
	nextWanted := shards - 1
	var streamErr error
	for i := 0; i < shards; i++ {
		r := <-results
		if r.err != nil {
			if streamErr == nil {
				streamErr = r.err
			}
			continue
		}
		pending[r.idx] = r.commits
		if streamErr != nil {
			continue
		}
		for {
			cs, ok := pending[nextWanted]
			if !ok {
				break
			}
			delete(pending, nextWanted)
			done := nextWanted == 0
			out <- LoadBatch{Commits: cs, Total: total, Done: done}
			nextWanted--
		}
	}
	wg.Wait()
	if streamErr != nil {
		out <- LoadBatch{Done: true, Err: streamErr}
	}
}
