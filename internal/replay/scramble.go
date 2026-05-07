package replay

import "unicode"

// ScrambleConfig controls the optional "movie decoder" effect layered
// on top of the typing animation. With it on, each line being typed
// renders as locked-prefix + a short stretch of noise that reshuffles
// every frame and snaps to the real character once the cursor reaches
// it. Off (zero value) is plain typing.
type ScrambleConfig struct {
	Enabled bool
	// RevealAhead is how many runes of noise are drawn after the
	// locked prefix. Higher = more "decoding" feel but visually
	// busier. Capped to the remaining line length at render time.
	RevealAhead int
	// Charset is the rune pool the noise is drawn from. Empty falls
	// back to DefaultScrambleCharset. Picking a pool with a similar
	// visual density to the real text reads more like "decoding".
	Charset []rune
}

// DefaultScrambleCharset is the default noise pool: a mix of letters,
// digits, and symbols that gives a "decoder" look without wandering
// into glyphs that may not render in every terminal font.
var DefaultScrambleCharset = []rune("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789!@#$%^&*+=<>?/|{}[]~")

// DefaultScrambleAhead is the recommended RevealAhead.
const DefaultScrambleAhead = 6

// PartialLine returns the visible prefix of an added line based on
// how many runes have been "typed". We split on token boundaries so
// that letters within an identifier appear together while punctuation
// acts as a visible breakpoint — reads as "human typing" rather than
// "scrolling".
func PartialLine(text string, chars int) string {
	if chars <= 0 {
		return ""
	}
	r := []rune(text)
	if chars >= len(r) {
		return text
	}
	stop := chars
	for i := chars; i < len(r) && i < chars+2; i++ {
		if !isWord(r[i-1]) {
			break
		}
		if !isWord(r[i]) {
			stop = i
			break
		}
	}
	if stop > len(r) {
		stop = len(r)
	}
	return string(r[:stop])
}

// ScrambleLine is PartialLine's "movie" sibling. Returns the locked
// prefix (token-boundary aligned, like PartialLine) followed by up to
// cfg.RevealAhead runes of pseudo-random noise picked from cfg.Charset
// using `seed`. Anything past that range is hidden, so the user never
// sees the un-typed real text.
//
// The seed is XORed with the rune position so each character flickers
// independently — calling this with a different seed every frame
// produces the shimmer; with the same seed the result is stable
// (useful for tests).
//
// Calling with cfg.Enabled=false collapses to PartialLine, so callers
// can branch once on the option and not at every line.
func ScrambleLine(text string, chars int, seed int64, cfg ScrambleConfig) string {
	if !cfg.Enabled {
		return PartialLine(text, chars)
	}
	r := []rune(text)
	if chars >= len(r) {
		return text
	}
	if chars < 0 {
		chars = 0
	}
	stop := chars
	// Only extend at a token boundary when we already have a typed
	// rune to look back at. With chars=0 there is no prefix to
	// "round forward" — the whole line is still in the noise zone.
	if chars > 0 {
		for i := chars; i < len(r) && i < chars+2; i++ {
			if !isWord(r[i-1]) {
				break
			}
			if !isWord(r[i]) {
				stop = i
				break
			}
		}
		if stop > len(r) {
			stop = len(r)
		}
	}

	pool := cfg.Charset
	if len(pool) == 0 {
		pool = DefaultScrambleCharset
	}
	ahead := cfg.RevealAhead
	if ahead < 0 {
		ahead = 0
	}
	end := stop + ahead
	if end > len(r) {
		end = len(r)
	}

	out := make([]rune, 0, end)
	out = append(out, r[:stop]...)
	for i := stop; i < end; i++ {
		out = append(out, pool[scrambleIndex(seed, i, len(pool))])
	}
	return string(out)
}

// scrambleIndex hashes (seed, position) into the charset. splitmix64-
// style: cheap and well-distributed, mixed with `i` so neighbouring
// positions don't move in lockstep.
func scrambleIndex(seed int64, i, mod int) int {
	s := uint64(seed)*6364136223846793005 + uint64(i)*1442695040888963407 + 1
	s ^= s >> 30
	s *= 0xbf58476d1ce4e5b9
	s ^= s >> 27
	s *= 0x94d049bb133111eb
	s ^= s >> 31
	return int(s>>33) % mod
}

func isWord(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}
