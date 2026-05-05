// Package model defines renderer-agnostic data types used by both the
// TUI and HTML outputs. A History is a sequence of Commits; each Commit
// carries the changed files for that commit and a BranchTag indicating
// which branch the commit originally lived on (best-effort, derived
// from a first-parent walk and merge-base split).
package model

import "time"

// BranchTag marks where in the history a commit "comes from", relative
// to the user's --against branch. Concretely, when replaying feat
// against main, commits reachable from feat but not from main are tagged
// Feature, and the rest (the parent branch) are tagged Against.
type BranchTag int

const (
	BranchTagUnknown BranchTag = iota
	// BranchTagFeature: commit is on the target branch only (after merge-base).
	BranchTagFeature
	// BranchTagAgainst: commit is on the --against branch (at or before merge-base).
	BranchTagAgainst
)

func (t BranchTag) String() string {
	switch t {
	case BranchTagFeature:
		return "feature"
	case BranchTagAgainst:
		return "against"
	default:
		return "unknown"
	}
}

// History is the full replay payload.
type History struct {
	// Branch is the user-supplied target branch (e.g. "feat/xyz").
	Branch string
	// Against is the parent branch used as the split point (e.g. "main").
	Against string
	// SubDir is the optional path filter applied at load time.
	SubDir string
	// Commits is ordered oldest -> newest (frame 0 is the oldest commit).
	Commits []Commit
}

// Commit is a single frame in the replay.
type Commit struct {
	Hash       string    // full sha
	ShortHash  string    // 7-12 chars
	Author     string    // "Name <email>"
	AuthorName string    // just the name
	When       time.Time // author time
	Subject    string    // first line of the commit message
	Body       string    // rest of the commit message
	Tag        BranchTag
	Files      []FileChange
}

// FileChange describes one file's diff in a single commit.
type FileChange struct {
	Path    string // current path
	OldPath string // for renames; empty otherwise
	Status  ChangeStatus
	Added   int // lines added in this commit
	Removed int // lines removed in this commit
	Hunks   []Hunk
}

type ChangeStatus int

const (
	StatusModified ChangeStatus = iota
	StatusAdded
	StatusDeleted
	StatusRenamed
	StatusCopied
)

func (s ChangeStatus) String() string {
	switch s {
	case StatusAdded:
		return "A"
	case StatusDeleted:
		return "D"
	case StatusRenamed:
		return "R"
	case StatusCopied:
		return "C"
	default:
		return "M"
	}
}

// Hunk is a contiguous diff region within a file.
// Lines are stored with their leading marker preserved ('+', '-', ' ').
type Hunk struct {
	OldStart int
	OldLines int
	NewStart int
	NewLines int
	Header   string // the "@@ ... @@" line (without the trailing context)
	Lines    []DiffLine
}

type DiffLineKind int

const (
	LineContext DiffLineKind = iota
	LineAdded
	LineRemoved
)

type DiffLine struct {
	Kind DiffLineKind
	Text string // line content without the leading +/-/space
}
