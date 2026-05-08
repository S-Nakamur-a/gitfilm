# Records the README demo gif. Run with:
#
#   vhs demo.tape
#
# Requires github.com/charmbracelet/vhs (`brew install vhs`).
# Output lands at docs/demo.gif. The tape is intentionally self-
# contained: it builds the binary, points git-film at this very
# repository's main branch (split at HEAD~14 so the timeline shows
# both feat-tagged and against-tagged commits), lets the animation
# play for ~5s (long enough to see the typing + caret sweep but
# small enough to keep the gif tiny), and quits cleanly. Adjust
# BRANCH / AGAINST / MAX below if you want a different slice.

Output docs/demo.gif

Set Theme "Catppuccin Mocha"
# Menlo is preinstalled on macOS and renders box-drawing glyphs cleanly.
# If you'd rather use JetBrains Mono, install it first
# (`brew install --cask font-jetbrains-mono`) and switch this line —
# vhs falls back to a generic monospace if the font is missing.
Set FontFamily "Menlo"
Set FontSize 14
# 1200x720 keeps the right pane diff cards readable. With a short
# (~5s) play loop the resulting gif lands well under 1 MB so we
# have plenty of headroom on GitHub's 10 MB embed cap.
Set Width 1200
Set Height 720
Set Padding 20
Set TypingSpeed 40ms
Set PlaybackSpeed 1.0

# Build the binary off-camera so the recording starts on a clean prompt.
# vhs's default minimal shell uses ">" as its PS1, so we can't easily
# Wait+Screen /\$/ for build completion — just give it a generous Sleep.
Hide
Type "make build"
Enter
Sleep 8s
Type "clear"
Enter
Sleep 500ms
Show

# The actual demo: 28-commit slice of gitfilm itself, split at HEAD~14
# so the bottom timeline alternates feat (pink) and against (blue) tags.
Type "./git-film main --against HEAD~14 --max 28"
Sleep 600ms
Enter

# Let the playback breathe. 5s is enough to show parallel typing in
# the right pane, the tree heatmap heating up, and the caret moving
# along the timeline with "Ny ago / today" axis labels. Longer plays
# bloat the gif quickly (frames × pixels × palette) without adding
# much new information — autoplay loops, so viewers see more anyway.
Sleep 5s

# Quit cleanly so the final frame isn't a half-rendered dwell.
Type "q"
Sleep 500ms
