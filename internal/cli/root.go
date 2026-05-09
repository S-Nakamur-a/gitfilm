package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/S-Nakamur-a/gitfilm/internal/gitlog"
	"github.com/S-Nakamur-a/gitfilm/internal/output"
	"github.com/S-Nakamur-a/gitfilm/internal/stats"
	"github.com/S-Nakamur-a/gitfilm/internal/tui"
	"github.com/spf13/cobra"
)

type options struct {
	against       string
	mode          string
	repo          string
	subdir        string
	htmlOut       string
	maxN          int
	authors       []string
	stats         bool
	verbose       bool
	scramble      bool
	scrambleAhead int
	colorMode     string
	seedTree      string
	showVendored  bool
	showGenerated bool
}

func New(version string) *cobra.Command {
	var opts options
	cmd := &cobra.Command{
		Use:     "git-film <branch>",
		Short:   "Replay your git history as an animation",
		Version: version,
		Long: "git-film walks a branch with --first-parent, tags commits by their\n" +
			"originating branch (split at merge-base with --against), and replays\n" +
			"the diffs as an animation in the terminal or a single HTML file.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Don't dump the usage banner when the run itself fails —
			// that floods over the actual error message and any
			// actionable hint it carries (e.g. "no commits matched").
			// Setting it inside RunE leaves usage enabled for the
			// arg-count / flag-parsing errors that fire BEFORE RunE,
			// where the usage text genuinely helps.
			cmd.SilenceUsage = true
			return run(args[0], opts)
		},
	}
	cmd.Flags().StringVar(&opts.against, "against", "main", "branch considered the parent (merge-base split point)")
	cmd.Flags().StringVarP(&opts.mode, "output", "o", "tui", "output mode: "+strings.Join(output.Names(), " | "))
	cmd.Flags().StringVar(&opts.repo, "repo", ".", "path to the git repository")
	cmd.Flags().StringVar(&opts.subdir, "path", "", "limit to changes under this subdirectory (relative to repo root)")
	cmd.Flags().StringVar(&opts.htmlOut, "html-out", "gitfilm.html", "html output file path (when -o html)")
	cmd.Flags().IntVar(&opts.maxN, "max", 0, "limit to the most recent N commits (0 = no limit)")
	cmd.Flags().StringSliceVar(&opts.authors, "author", nil, "filter to commits whose author name or email matches `regex` (repeatable; comma-separated values are OR'd; values pass through to git log --author)")
	cmd.Flags().BoolVar(&opts.stats, "stats", false, "print load time, dwell distribution, and per-commit stats; do not render")
	cmd.Flags().BoolVarP(&opts.verbose, "verbose", "v", false, "log per-stage timings and memory to stderr (always on for non-tui modes)")
	cmd.Flags().BoolVar(&opts.scramble, "scramble", false, "render the typing animation as a 'movie decoder' effect: noisy characters that snap to the real text as they're typed")
	cmd.Flags().IntVar(&opts.scrambleAhead, "scramble-ahead", 0, "with --scramble, how many runes of noise to draw ahead of the cursor (0 = default)")
	cmd.Flags().StringVar(&opts.colorMode, "color-mode", "gradient", "timeline shading: gradient (truecolor brightness ramp per tag, default) | glyph (5-level quartile glyphs, for 16-color or low-fidelity terminals)")
	cmd.Flags().StringVar(&opts.seedTree, "seed-tree", "auto", "pre-populate the TUI tree with files that exist before --max truncates: auto (on when --max>0) | on | off")
	cmd.Flags().BoolVar(&opts.showVendored, "show-vendored", false, "include linguist-vendored paths (vendor/, node_modules/) in the seed")
	cmd.Flags().BoolVar(&opts.showGenerated, "show-generated", false, "include linguist-generated paths (lockfiles, codegen) in the seed")
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
		fmt.Fprintf(os.Stderr, "[gitfilm] starting: branch=%s against=%s mode=%s max=%d subdir=%q authors=%v\n",
			branch, opts.against, opts.mode, opts.maxN, opts.subdir, opts.authors)
	}

	req := gitlog.LoadRequest{
		Branch:  branch,
		Against: opts.against,
		SubDir:  opts.subdir,
		MaxN:    opts.maxN,
		Authors: opts.authors,
	}

	// TUI takes the streaming path: first paint happens as soon as the
	// oldest shard arrives instead of after the full Load. Skips the
	// renderer registry because output.Renderer.Run wants a complete
	// History upfront, which defeats the point.
	colorMode, err := tui.ParseColorMode(opts.colorMode)
	if err != nil {
		return err
	}

	if opts.mode == "tui" && !opts.stats {
		seedPaths, err := resolveSeed(loader, req, opts)
		if err != nil {
			// Seed failure is informational only — fall through with no
			// seed rather than aborting the whole run.
			fmt.Fprintf(os.Stderr, "[gitfilm] seed: %v (continuing without seed)\n", err)
			seedPaths = nil
		}
		return tui.RunStream(loader, req, tui.Options{
			Scramble:      opts.scramble,
			ScrambleAhead: opts.scrambleAhead,
			ColorMode:     colorMode,
			SeedPaths:     seedPaths,
		})
	}

	loadStart := time.Now()
	frames, err := loader.Load(req)
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
		stats.Print(os.Stderr, frames, loadDur)
		return nil
	}

	r, ok := output.Get(opts.mode)
	if !ok {
		return fmt.Errorf("unknown output mode %q (want one of: %s)", opts.mode, strings.Join(output.Names(), ", "))
	}
	cfg := output.Config{
		HTMLOutPath:   opts.htmlOut,
		Scramble:      opts.scramble,
		ScrambleAhead: opts.scrambleAhead,
	}
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

// resolveSeed turns the user-facing --seed-tree / --show-vendored /
// --show-generated flags into a concrete path list. Returns (nil, nil)
// when seeding is disabled (either explicitly via --seed-tree=off or
// implicitly when --max=0 leaves no "before the window" to describe).
func resolveSeed(loader *gitlog.Loader, req gitlog.LoadRequest, opts options) ([]string, error) {
	enabled := false
	switch opts.seedTree {
	case "on":
		enabled = true
	case "off":
		return nil, nil
	case "", "auto":
		enabled = req.MaxN > 0
	default:
		return nil, fmt.Errorf("unknown --seed-tree %q (want auto|on|off)", opts.seedTree)
	}
	if !enabled {
		return nil, nil
	}
	res, err := loader.LoadSeed(req, gitlog.SeedOptions{
		IncludeVendored:  opts.showVendored,
		IncludeGenerated: opts.showGenerated,
	})
	if err != nil {
		return nil, err
	}
	return res.Paths, nil
}
