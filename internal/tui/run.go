package tui

import (
	"fmt"
	"io"
	"strings"

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
//
// Peeks the first batch synchronously so a request that resolves to
// zero commits (e.g. `--author=me` matching no author header lines)
// fails fast with an actionable error instead of launching the
// alt-screen TUI and sitting on "loading commits…" forever — the
// loader signals empty results by emitting a single
// LoadBatch{Total: 0, Done: true} sentinel, which the TUI's View()
// can't distinguish from "still loading" because it only checks
// len(history.Commits) == 0. Mirrors the non-streaming path's behavior
// in cli/root.go.
func RunStream(loader *gitlog.Loader, req gitlog.LoadRequest, opts Options) error {
	ch := loader.LoadStream(req)
	first, ok := <-ch
	if !ok {
		// Loader closed without sending anything. Shouldn't happen by
		// protocol (loadStream always emits at least one batch), but
		// be explicit instead of falling through to a blank TUI.
		return fmt.Errorf("loader closed without sending any batches")
	}
	if first.Err != nil {
		return first.Err
	}
	if first.Done && len(first.Commits) == 0 {
		return emptyResultError(req)
	}
	// Re-feed the peeked batch so the TUI consumes the stream as if
	// nothing had been read. Buffered to match LoadStream's own
	// channel capacity so the forwarder goroutine never blocks.
	proxy := make(chan gitlog.LoadBatch, 16)
	proxy <- first
	go func() {
		defer close(proxy)
		for b := range ch {
			proxy <- b
		}
	}()
	return runStreamingProgram(req.Branch, req.Against, proxy, opts)
}

// emptyResultError builds the actionable "no commits matched" message
// for the streaming entry point. Echoes the active filters so the user
// can see at a glance which knob excluded everything, and adds a hint
// about --author's regex semantics — that's the most common surprise
// (e.g. `--author=me` matches the literal substring "me" in name OR
// email, NOT the current user, and won't match anything if no author
// header contains the bytes "me").
func emptyResultError(req gitlog.LoadRequest) error {
	parts := []string{fmt.Sprintf("branch=%q", req.Branch)}
	if len(req.Authors) > 0 {
		parts = append(parts, fmt.Sprintf("--author=%s", strings.Join(req.Authors, ",")))
	}
	if req.SubDir != "" {
		parts = append(parts, fmt.Sprintf("--path=%q", req.SubDir))
	}
	if req.MaxN > 0 {
		parts = append(parts, fmt.Sprintf("--max=%d", req.MaxN))
	}
	msg := "no commits matched (" + strings.Join(parts, ", ") + ")"
	if len(req.Authors) > 0 {
		msg += "\nhint: --author is a regex matched against the author name OR email, " +
			"not the current user — e.g. `--author=me` only matches authors whose name " +
			"or email literally contains \"me\""
	}
	return fmt.Errorf("%s", msg)
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
