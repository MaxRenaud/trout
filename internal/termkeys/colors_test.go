package termkeys

import "testing"

func TestColors(t *testing.T) {
	for _, c := range []struct {
		name string
		env  map[string]string
		want int
	}{
		{"iTerm2", map[string]string{"TERM": "xterm-256color", "COLORTERM": "truecolor"}, TrueColor},
		{"GNU screen, truecolor passed through", map[string]string{"TERM": "screen.xterm-256color", "STY": "123.pts-0.pi", "COLORTERM": "truecolor"}, Colors256},
		{"ssh from inside screen", map[string]string{"TERM": "screen"}, Colors256},
		{"tmux", map[string]string{"TERM": "screen-256color", "TMUX": "/tmp/tmux-1000/default,1,0", "COLORTERM": "truecolor"}, TrueColor},
		{"PuTTY", map[string]string{"TERM": "xterm"}, TrueColor},
		{"PuTTY, 256 colors", map[string]string{"TERM": "xterm-256color"}, TrueColor},
		{"Terminal.app", map[string]string{"TERM": "xterm-256color", "TERM_PROGRAM": "Apple_Terminal"}, TrueColor},
		{"kitty", map[string]string{"TERM": "xterm-kitty"}, TrueColor},
		{"Windows console", map[string]string{}, TrueColor},
		{"Windows Terminal over ssh", map[string]string{"TERM": "xterm-256color", "WT_SESSION": "x"}, TrueColor},
		{"Linux console", map[string]string{"TERM": "linux"}, Colors16},
		{"override", map[string]string{"TERM": "screen", "STY": "1", ColorsEnv: "24bit"}, TrueColor},
		{"override down", map[string]string{"TERM": "xterm-kitty", ColorsEnv: "16"}, Colors16},
	} {
		if got := Colors(func(k string) string { return c.env[k] }); got != c.want {
			t.Errorf("%s: %d colors, want %d", c.name, got, c.want)
		}
	}
}
