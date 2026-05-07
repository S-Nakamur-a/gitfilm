package gitlog

import (
	"runtime"
	"time"

	"github.com/S-Nakamur-a/gitfilm/internal/model"
)

// targetShardSize is the commits-per-shard target. Each shard must do
// enough work to amortize the fork/exec cost of `git log` (~10ms) but
// not so much that one slow shard holds up the rest of the load. ~1000
// commits/shard hits a good balance on monorepos in informal testing.
const targetShardSize = 1000

// maxShardWorkers caps fan-out beyond which disk contention erodes the
// gains from extra git processes.
const maxShardWorkers = 8

// shardPlan describes how Load / LoadStream chunk the commit range.
// Both pipelines compute it identically; sharing the helper keeps the
// math in one place.
type shardPlan struct {
	Shards    int
	Workers   int
	ShardSize int
}

// planShards returns the plan for `total` commits. `total <= 0` yields
// a zero plan (no work) so callers can short-circuit.
func planShards(total int) shardPlan {
	if total <= 0 {
		return shardPlan{}
	}
	workers := runtime.NumCPU()
	if workers > maxShardWorkers {
		workers = maxShardWorkers
	}
	shards := (total + targetShardSize - 1) / targetShardSize
	if shards < workers {
		workers = shards
	}
	if workers < 1 {
		workers = 1
	}
	shardSize := (total + shards - 1) / shards
	return shardPlan{Shards: shards, Workers: workers, ShardSize: shardSize}
}

// shardWindow returns the [skip, take) range for shard index `i`.
func (p shardPlan) Window(i, total int) (skip, take int) {
	skip = i * p.ShardSize
	take = p.ShardSize
	if skip+take > total {
		take = total - skip
	}
	return skip, take
}

// shardOutput collects the per-shard timing telemetry. Only the
// synchronous Load path logs these; LoadStream is free to ignore them.
type shardOutput struct {
	Commits  []model.Commit
	GitDur   time.Duration
	ParseDur time.Duration
	Bytes    int
}

// applyTags labels commits according to the feature set produced by
// `featureSet`. A nil set tags every commit Feature (graceful
// degradation when --against can't be resolved). Mutates `commits`
// in place.
func applyTags(commits []model.Commit, featureSet map[string]struct{}) {
	for i := range commits {
		switch {
		case featureSet == nil:
			commits[i].Tag = model.BranchTagFeature
		default:
			if _, ok := featureSet[commits[i].Hash]; ok {
				commits[i].Tag = model.BranchTagFeature
			} else {
				commits[i].Tag = model.BranchTagAgainst
			}
		}
	}
}

// reverseCommits flips the slice in place. The loader walks newest-
// first, so this brings shards (or the full slice) into oldest-first
// order before delivery.
func reverseCommits(commits []model.Commit) {
	for i, j := 0, len(commits)-1; i < j; i, j = i+1, j-1 {
		commits[i], commits[j] = commits[j], commits[i]
	}
}
