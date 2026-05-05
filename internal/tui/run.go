package tui

import (
	"io"

	"github.com/S-Nakamur-a/gitfilm/internal/gitlog"
	"github.com/S-Nakamur-a/gitfilm/internal/model"
	"github.com/S-Nakamur-a/gitfilm/internal/output"
)

// Run is the historical entrypoint for fully-loaded histories. Kept
// for callers that already have a History in hand (e.g. tests).
func Run(h model.History) error {
	return runProgram(h)
}

// RunStream drives the TUI off a streaming loader: the first frame
// paints as soon as the OLDEST shard arrives (~1s on a 7.9k-commit
// monorepo) instead of after the full 4-5s synchronous Load. New
// commits append to history as later batches land, and autoplay
// auto-resumes when it had stalled at the previously-loaded end.
//
// Wires the loader and the TUI program directly so the CLI doesn't
// need to know about the channel plumbing.
func RunStream(loader *gitlog.Loader, req gitlog.LoadRequest) error {
	ch := loader.LoadStream(req)
	return runStreamingProgram(req.Branch, req.Against, ch)
}

// renderer adapts the TUI to the output.Renderer interface for the
// non-streaming path. The CLI special-cases TUI for streaming, so
// this is mostly here to keep the output.Names() listing honest.
type renderer struct{}

func (renderer) Run(h model.History, _ output.Config, _ io.Writer) error {
	return runProgram(h)
}

func init() {
	output.Register("tui", renderer{})
}
