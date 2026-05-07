# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this project is

`gitfilm` is a Go CLI (`git-film`, invokable as `git film …` because git auto-routes
unknown subcommands to a `git-<name>` binary) that replays a branch's history as
either a Bubble Tea TUI animation or a single self-contained HTML file. The
binary walks the target branch with `--first-parent`, splits commits at the
merge-base with `--against`, and animates per-file diffs.

## Common commands

```sh
make build                        # build the binary into ./git-film
make install                      # install onto $GOBIN as git-film
go run ./cmd/git-film <branch>  # quick local run (TUI)
make test                         # run the full test suite (= go test ./...)
go test ./internal/tui -run TestStep_Rename   # single test by name
go vet ./...
gofmt -l .                        # list files needing formatting
```

Useful runtime flags (full list in `internal/cli/root.go`):

- `--against main` — branch used as the merge-base split point.
- `-o tui|html` — output mode (default `tui`); `--html-out` controls the file path.
- `--max 500` — cap commits loaded; `0` = no limit.
- `--path <subdir>` — restrict to a subtree.
- `--stats` — print load time, dwell distribution, per-commit stats, and exit
  without rendering. Use this when tuning loader/animation perf — it is the
  measurement harness the codebase was tuned against.

Go version: **1.25** (see `go.mod`). Dependencies: `bubbletea`, `lipgloss`,
`cobra`. No vendored modules.

## Security invariant: zero external network egress

This tool reads `.git` contents (commit messages, diffs, author identities,
private branch names, possibly secrets accidentally committed) and must
**never** send any of it off the host. Treat this as a hard invariant, not a
guideline:

- **No Go network APIs.** The Go binary must not import `net/http`,
  `net/url`'s client, `net`-level dialers, mail/SMTP/etc., or any third-party
  library that does. The only allowed external process is `git` itself
  (`exec.Command("git", ...)` in `internal/gitlog/loader.go`). Do not shell
  out to `curl`/`wget`/`nc`/etc.
- **HTML output is fully self-contained.** The single `gitfilm.html` file
  must work on an air-gapped machine opened from disk. That means
  `internal/htmlout/template.html` and the streamed `<script id="chunk-N">`
  tags must contain **zero** outbound references: no `<link>`, no
  `<script src=…>`, no `<iframe>`/`<img>`/`<video>`/`<audio>`/`<source>`
  with remote URLs, no `@font-face`, no `@import`, no `url(...)` pointing
  off-origin, no CDN. Fonts use system stacks only
  (`ui-monospace, …, monospace` / `system-ui`).
- **Player JS must not phone home.** No `fetch`, `XMLHttpRequest`,
  `WebSocket`, `EventSource`, `navigator.sendBeacon`, `Image()` ping
  patterns, or third-party analytics. The player only reads JSON out of
  inline `<script>` tags via `document.getElementById(...).textContent` and
  manipulates the DOM. Storage APIs (`localStorage`, `sessionStorage`,
  `indexedDB`, `document.cookie`) are not currently used and should stay
  unused — they don't egress, but they persist diff bytes to disk where
  another tab could read them.
- **HTML escaping in the payload is load-bearing.** `encodeMeta` and
  `streamChunks` both call `enc.SetEscapeHTML(true)`; that is what stops a
  crafted commit message from breaking out of the inline `<script>` and
  smuggling a remote-loading tag into the page. Never flip this to `false`
  for "smaller output" — the savings aren't worth it and the security
  property is gone the moment you do.

When adding a backend or touching `template.html`, re-verify with a quick
grep: imports of `net/http`, occurrences of `http://`/`https://`/`ws://`,
and `<link`/`<script src`/`<iframe`/`@import`/`@font-face` should all be
zero across the repo.

## Architecture (read this before making cross-package changes)

The data model is **renderer-agnostic**, and playback policy lives in a
**dedicated package** so every renderer derives pacing/heat from the same
source. Those are the two central design constraints.

```
cmd/git-film
   └─ blank-imports internal/htmlout, internal/tui  (registers them)
   └─ internal/cli           Cobra command, flag parsing, dispatches to renderers
        └─ internal/output   Renderer interface + name registry
        └─ internal/gitlog   shells out to `git log -p`, parses to model.History
              └─ internal/model    History/Commit/FileChange/Hunk/DiffLine
        └─ internal/replay   playback policy: anim/scramble, dwell, segments, TreeState
              ├─ internal/tui     Bubble Tea program (consumes replay)
              └─ internal/htmlout single-file HTML (precomputes via replay)
        └─ internal/stats    --stats report writer (consumes replay.DwellFor)
```

`model.History` is the contract between the loader and any renderer. The
`internal/output` package is the contract between the CLI and any
renderer — backends self-register from `init()` so adding a format means
adding one package and one blank-import in `cmd/git-film/main.go`. The
`cli` package no longer imports backends directly.

### Loader (`internal/gitlog`)

- Uses the `git` CLI (not a Go-native git library) on purpose: matching git's
  exact `--first-parent` / merge-base / patch behavior in another implementation
  is error-prone. The loader's `Runner` interface is the seam tests use to
  inject canned output.
- `loadCommitsBatched` shards `git log -p` across goroutines
  (`runtime.NumCPU()`, capped at 8) using `--skip M -n K`. This is the reason
  large monorepos load fast — fork/exec cost is paid per shard, not per commit.
  Tune `targetShard` (currently 1000 commits) only with the `--stats` harness.
- The parser uses **marker lines** (`__GITFILM_BEGIN__` / `__GITFILM_BODY__` /
  `__GITFILM_END__`) wrapped via `--format=...` to separate metadata from diff
  output. Don't switch to `--numstat`: the TUI's typing animation needs full
  hunk text.
- `parseFileDiffs` enforces **soft caps** (`maxLinesPerHunk`, `maxHunksPerFile`,
  `maxFilesPerCommit` in `diffparse.go`). These bound memory on pathological
  commits without changing what the user sees.
- `git log` is invoked with `--unified=1` to keep context lines minimal.
- `LoadStream` (in `streaming.go`) is the progressive variant: same
  shard plan as `loadCommitsBatched`, but workers tag + reverse each
  shard then send into a `results` channel, and a delivery goroutine
  emits batches in **oldest-first shard order** (highest shard index
  first). Out-of-order shard completions buffer in `pending`. The
  channel returned by `LoadStream` carries `LoadBatch{Commits, Total,
  Done, Err}` and is closed when streaming ends. Used by the TUI for
  ~1s first paint instead of waiting on the slowest shard. The HTML
  renderer and `--stats` keep using the synchronous `Load`.

### Model (`internal/model`)

`History` is `Commits` ordered **oldest → newest**. `BranchTag` is `Feature`
(commits reachable from `Branch` but not `Against`) or `Against` (everything
else). If the `--against` branch can't be resolved, every commit is tagged
`Feature` (graceful degradation).

The model package holds **only data types** — no playback math, no
heat-decay state. Anything that "interprets" a `History` lives in
`internal/replay`.

### Playback policy (`internal/replay`)

This is the package both renderers depend on. It owns:

- **Animation cost** (`anim.go`): `LineCost`, `HunkGap`, `MinFileBudget`,
  `FileBudget`/`FileBudgetWith`, `ApplyFile`/`ApplyFileWith`,
  `CommitMaxBudget*`. Cost constants and the cursor algorithm live here so
  the TUI's typing animation and the HTML player burn budget at exactly
  the same rate.
- **Per-line typing/scramble** (`scramble.go`): `PartialLine`, `ScrambleLine`,
  `ScrambleConfig`, `DefaultScrambleCharset`, `DefaultScrambleAhead`. Split
  out from `anim.go` so the budget math and the line-rendering helpers
  evolve independently — different consumers, different change cadence.
- **Visibility profiles**: `FullProfile` (TUI shows every hunk) and
  `FirstHunkProfile` (HTML shows the first hunk's first
  `VisibleLinesPerHunkHTML` lines). Pass a profile when the renderer
  shows less than everything so dwell time matches what the user sees.
- **Pacing** (`dwell.go`): `UnitsPerSecond`, `MinCommitMS`, `MaxCommitMS`,
  `DwellFor`/`DwellForWith`. They are calibrated together; bump one in
  isolation and dwell feel breaks — re-run `--stats` after changes.
- **Branch segments** (`segments.go`): `Segments(commits)` collapses runs
  of equal `BranchTag` for the timeline strip.
- **Tree state** (`tree.go`): `TreeState` tracks per-file heat with
  exponential decay (`DefaultHalfLife = 7`), tracks `added`/`deleted`/
  `statuses`, materializes a filtered `TreeNode` via `Snapshot`/
  `SnapshotWith`, and exposes `HeatSnapshot` for JSON serialization.
  `Clone()` powers the TUI's backward-navigation cache.

When tuning playback (cost, half-life, dwell), edit `internal/replay`
once — both backends pick it up.

### TUI (`internal/tui`)

- Bubble Tea + Lipgloss. Entries:
  - `Run(History)` — fully-loaded history (compat / tests).
  - `RunStream(loader, req)` — streaming, used by the CLI.
  - `output.Renderer` (registered in `init()`) — non-streaming fallback.
  Imports `internal/replay` for all playback math and `internal/gitlog`
  for the streaming `LoadBatch` type.
- File layout — each file is a focused concern, ~100–200 lines:
  - `program.go` — entry points (`runProgram`, `runStreamingProgram`),
    package doc.
  - `layout.go` — every constant that controls screen shape & pacing
    (pane minima, card height, snapshot interval, `playSpeedSteps`,
    `readTail`, `frameTickMS`). Single source of truth shared between
    dwell calc and rendering.
  - `model.go` — `programModel` struct, constructors, Bubble Tea wiring
    (`tickMsg`, `batchMsg`, `Init`).
  - `update.go` — `Update` reducer, key/tick/batch handlers, `applyBatch`.
  - `nav.go` — `advance`, `jumpTo`, snapshot cache helpers.
  - `pacing.go` — `effectiveElapsed`, `commitProgress`, `computeDwell`,
    `expandableCount`, `bumpPlaySpeed`.
  - `view.go` — top-level `View()` + chrome (header, subject, footer).
  - `view_tree.go` — left-pane filesystem tree.
  - `view_right.go` — right-pane commit + file cards (incl.
    `cardLineWindow` for tail-scroll).
  - `view_timeline.go` — bottom strip (density, quartile shading,
    commit progress bar).
  - `style.go` — module-level styles, `heatNameStyle`, `tagLabel`,
    `statusBadge`.
  - `util.go` — `clipPane`, `truncate`, `firstNonEmptyLine`, `pluralS`.
- **Streaming consumption**: `programModel.loadCh` is non-nil when the
  program was started via `RunStream`. `Init` arms a `waitForBatch` Cmd;
  each `batchMsg` calls `applyBatch` which appends commits to history,
  steps the head tree, and populates snapshot buckets. Single-threaded
  on the Bubble Tea event-loop goroutine, so no locking. First paint is
  bound by the OLDEST shard's completion, not the full Load.
- **Two tree states**: `m.tree` tracks the user's current `idx`;
  `m.headTree` tracks the deepest loaded commit so snapshot bucketing
  stays correct as commits stream in.
- **Auto-resume at stream end**: when autoplay reaches the loaded end
  while still loading, `pausedAtEnd` flags it and the next batch
  unpauses so playback flows naturally as more commits arrive.
- **Backward-navigation cache**: `m.snapshots[]` stores a
  `replay.TreeState.Clone()` every `snapshotInterval` (100) commits.
  Jumping back rewinds to the nearest snapshot and replays at most that
  many commits instead of the whole prefix.
- **Visible-driven dwell**: `computeDwell` sizes dwell from the largest
  *visible* file's budget, not `replay.CommitMaxBudget` across all
  files. Hidden huge files would otherwise extend dwell while the
  visible cards finished typing in a fraction of it. Recomputed on
  resize since visible-card count is height-dependent.
- **Constant typing rate**: `units = elapsed_seconds × UnitsPerSecond ×
  playSpeed`. Previously `units = progress × maxBudget` made the rate
  diverge from calibration when dwell hit its Min/Max clamps.
- **No nested ANSI in pane content**: pre-rendered styled segments are
  concatenated, never fed back through another `.Render()` — the
  inner SGR-reset would close the outer style and bleed into adjacent
  panes. `clipPane` performs ANSI-aware horizontal truncation.

### HTML output (`internal/htmlout`)

**Offline / no-egress contract**: see "Security invariant" above. The output
file is meant to be openable on an air-gapped host straight from disk —
`template.html` may not introduce any outbound references, and the player JS
may not call `fetch`/`XHR`/`WebSocket`/`sendBeacon`/etc. All commit and diff
bytes ride inline in `<script>` tags and are read via `textContent`.

`template.html` is `//go:embed`ed and rendered with `html/template`. The
JSON payload is **precomputed in Go**: per-commit `dwell_ms` and
`max_budget`, per-file `budget`, per-frame `snapshot` (path/heat/touches/
ghosts/added arrays), plus a `tuning` block (`line_cost`, `hunk_gap`,
`half_life`, `hidden_below`, `faint_below`, `visible_lines`) sourced
from `internal/replay`. The browser-side JS is a player only — it
advances the typing cursor, reconstitutes Maps/Sets from the parallel
arrays, and updates the DOM. **It does not reimplement heat decay,
budget calculation, or dwell.** Editing `replay` constants flows
through automatically; do not duplicate them in `template.html`.

File layout:
- `render.go` — template parse + I/O writer + `Renderer` adapter
  (`Render`, `RenderWithDiag`, `streamChunks`, `executeHead`).
- `payload.go` — JSON shapes (`metaJSON`/`commitDetail`/`fileJSON`/
  `hunkJSON`/`snapshotJSON`/`tuningJSON`) and `buildPayloadTimed` walk.
- `template.html` — embedded HTML/CSS/JS (player only).

Field names are kept short (`k`, `t` for diff lines) because diff
payloads dominate file size.

### Renderer registry (`internal/output`)

- `Renderer` interface: `Run(History, Config, diag io.Writer) error`.
- `Register(name, Renderer)` is called from each backend's `init()`.
- `cli/root.go` dispatches batch-style outputs (HTML) via
  `output.Get(opts.mode)`. **TUI is special-cased** to use
  `tui.RunStream(loader, req, opts)` — the renderer interface wants a
  complete `model.History` upfront, which defeats progressive load.
  `cli/root.go` therefore imports `internal/tui` directly. The
  registered TUI renderer is intentionally kept as a non-streaming
  fallback so `output.Names()` still lists "tui" in the `--output` help.

## Conventions

- Package comments explain the *why* (see `model/model.go`, `gitlog/loader.go`).
  Match that style when adding a package.
- Tests use the `Runner` injection pattern (`gitlog.NewLoaderWithRunner`) for
  loader behavior, and direct construction for `TreeState` / `programModel`.
  No external test fixtures — everything is built inline.
- `internal/...` is enforced for everything that isn't `cmd/git-film/main.go`.
  Keep it that way unless someone genuinely needs to import the model.
