package tui

import (
	"io"

	"github.com/S-Nakamur-a/gitfilm/internal/gitlog"
	"github.com/S-Nakamur-a/gitfilm/internal/model"
	"github.com/S-Nakamur-a/gitfilm/internal/output"
)

// Run starts the TUI on a fully-loaded history. Used by the registered
// renderer adapter (non-streaming) and by tests that already hold a
// History. Pass Options{} for default behaviour.
func Run(h model.History, opts Options) error {
	return runProgram(h, opts)
}

// RunStream drives the TUI off a streaming loader: the first frame
// paints as soon as the OLDEST shard arrives (~1s on a 7.9k-commit
// monorepo) instead of after the full 4-5s synchronous Load. New
// commits append to history as later batches land, and autoplay
// auto-resumes when it had stalled at the previously-loaded end.
//
// Wires the loader and the TUI program directly so the CLI doesn't
// need to know about the channel plumbing.
func RunStream(loader *gitlog.Loader, req gitlog.LoadRequest, opts Options) error {
	ch := loader.LoadStream(req)
	return runStreamingProgram(req.Branch, req.Against, ch, opts)
}

// renderer adapts the TUI to the output.Renderer interface for the
// non-streaming path. The CLI special-cases TUI for streaming, so this
// adapter mainly exists so output.Names() still lists "tui" in the
// --output help text.
type renderer struct{}

func (renderer) Run(h model.History, cfg output.Config, _ io.Writer) error {
	return runProgram(h, Options{Scramble: cfg.Scramble, ScrambleAhead: cfg.ScrambleAhead})
}

func init() {
	output.Register("tui", renderer{})
}
