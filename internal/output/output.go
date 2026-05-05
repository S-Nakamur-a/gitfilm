// Package output defines the renderer abstraction shared by every
// output backend (TUI, HTML, future formats). Backends self-register
// from init() so the CLI can dispatch by name without importing each
// renderer directly — adding a new format means adding one package +
// one blank import in main.
package output

import (
	"io"
	"sort"

	"github.com/S-Nakamur-a/gitfilm/internal/model"
)

// Config carries optional per-renderer settings. Renderers ignore
// fields they don't care about.
type Config struct {
	// HTMLOutPath is the destination file for the html renderer. Empty
	// string falls back to the renderer's default ("gitfilm.html").
	HTMLOutPath string
}

// Renderer renders a model.History according to the given config.
// Implementations may take over the terminal (TUI), write to a file
// (HTML), or anything else; renderers that produce diagnostic output
// (e.g. "wrote gitfilm.html") write it to diag, leaving stdout free
// for any data they want to emit.
type Renderer interface {
	Run(h model.History, cfg Config, diag io.Writer) error
}

var registry = map[string]Renderer{}

// Register adds a renderer keyed by its mode name. Call from init().
// Panics on duplicate registration so wiring mistakes surface at
// startup instead of silently dropping one.
func Register(name string, r Renderer) {
	if _, exists := registry[name]; exists {
		panic("output: duplicate renderer name: " + name)
	}
	registry[name] = r
}

// Get returns the renderer for the given mode name.
func Get(name string) (Renderer, bool) {
	r, ok := registry[name]
	return r, ok
}

// Names returns all registered mode names in sorted order. Useful for
// CLI error messages ("unknown mode X, want one of: html, tui").
func Names() []string {
	out := make([]string, 0, len(registry))
	for k := range registry {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
