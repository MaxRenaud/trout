package termkeys

import "strings"

// How many colors a terminal shows.
const (
	Colors16  = 16
	Colors256 = 256
	TrueColor = 1 << 24
	ColorsEnv = "TROUT_COLORS" // 16, 256 or 24bit: overrides the guess
)

// Colors guesses how many colors the terminal shows, from the
// environment. trout draws in 24-bit color, which GNU screen 4 can't show:
// it reads 38;2;r;g;b as dim and the numbers as attributes (checked on
// screen 4.00), so screen gets 256 colors, which it understands (4.00
// turns them into its 16), even when COLORTERM, passed through from the
// terminal outside, says truecolor. tmux sets TMUX and shows 24-bit color.
// COLORTERM alone isn't enough to go by: PuTTY never sets it.
func Colors(getenv func(string) string) int {
	switch strings.ToLower(getenv(ColorsEnv)) {
	case "24bit", "truecolor", "16m":
		return TrueColor
	case "256":
		return Colors256
	case "16", "8":
		return Colors16
	}
	term := getenv("TERM")
	if getenv("STY") != "" || strings.HasPrefix(term, "screen") && getenv("TMUX") == "" {
		return Colors256 // GNU screen
	}
	switch {
	case term == "linux", term == "dumb", term == "ansi", strings.HasPrefix(term, "vt"), strings.HasPrefix(term, "cons"):
		return Colors16
	}
	// Everything else gets 24-bit, as before there was a choice: PuTTY
	// shows it but says TERM=xterm and no COLORTERM, and 256 colors turned
	// the title gradient into bands there; terminals that can't show it,
	// like Terminal.app before macOS 26, round it to their 256.
	return TrueColor
}
