# gitfilm

Replay your git history as an animation in the terminal — or as a single
self-contained HTML file you can share.

```sh
# install (places `git-film` onto $GOBIN)
make install
# or directly:
go install github.com/S-Nakamur-a/gitfilm/cmd/git-film@latest

# git automatically routes "git film ..." to the git-film binary.
git film feat/xyz --against main          # TUI (default)
git film feat/xyz --against main -o html  # writes gitfilm.html
git film feat/xyz --against main --stats  # load time / dwell distribution
```

## What it does

- Walks the target branch with `--first-parent`, splits commits at the
  merge-base with `--against` and color-codes the timeline so you can
  see "this part came from feat, this part came from main".
- **Left pane**: live directory tree with a per-file churn heatmap.
  Files cool off over time (exponential decay, ~7-commit half-life)
  and disappear from the tree when they go cold, so the panel stays
  focused on what's currently being touched.
- **Right pane**: the commit subject, author, and the changed files
  laid out as cards. All files in a commit type out in parallel,
  each at its own pace (so a 200-line refactor takes longer than a
  one-line typo).
- **Bottom**: a colored timeline scrubber and a legend explaining the
  colors.

The same data drives both the TUI and the HTML output, so the HTML
build is just `go run ./cmd/git-film -o html` away.

## Useful flags

| Flag | Default | Purpose |
| --- | --- | --- |
| `--against` | `main` | branch used as the merge-base split point |
| `-o, --output` | `tui` | `tui` or `html` |
| `--max` | `500` | cap commits loaded; `0` = no limit |
| `--path` | "" | only show changes under this subdirectory |
| `--repo` | `.` | path to the git repository |
| `--html-out` | `gitfilm.html` | output file when `-o html` |
| `--stats` | off | print load time / dwell distribution and exit |

## Performance notes

`git log -p` is sharded across goroutines so a ~8K-commit monorepo
loads in a few seconds instead of half a minute, with peak memory
~360 MB instead of ~1.3 GB. Backwards navigation in the TUI uses
periodic snapshots of the tree state so jumping back doesn't replay
the whole history. Per-hunk and per-file caps keep generated diff data
bounded even on commits that touch a thousand files.

## Status

Experimental. The frame model is renderer-agnostic; the TUI and HTML
both consume it.
