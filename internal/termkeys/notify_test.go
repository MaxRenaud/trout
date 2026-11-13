package termkeys

import "testing"

func TestNotifyFor(t *testing.T) {
	for _, c := range []struct {
		env  map[string]string
		want Notify
	}{
		{map[string]string{"TERM_PROGRAM": "iTerm.app"}, NotifyOSC9},
		{map[string]string{"LC_TERMINAL": "iTerm2", "TERM_PROGRAM": "tmux", "TMUX": "/tmp/x"}, NotifyOSC9},
		{map[string]string{"TERM": "xterm-kitty", "KITTY_WINDOW_ID": "1"}, NotifyOSC99},
		{map[string]string{"TERM_PROGRAM": "ghostty", "TERM": "xterm-ghostty"}, NotifyOSC777},
		{map[string]string{"TERM_PROGRAM": "WezTerm"}, NotifyOSC777},
		{map[string]string{"TERM": "foot"}, NotifyOSC777},
		{map[string]string{"TERM_PROGRAM": "Apple_Terminal"}, NotifyNone},
		{map[string]string{"WT_SESSION": "x"}, NotifyNone}, // Windows Terminal: OSC 9 is other things
		{map[string]string{"TERM": "alacritty"}, NotifyNone},
		{map[string]string{"TERM_PROGRAM": "iTerm.app", "STY": "1.pts"}, NotifyNone}, // GNU screen
		{map[string]string{}, NotifyNone},
	} {
		if got := NotifyFor(func(k string) string { return c.env[k] }); got != c.want {
			t.Errorf("%v: %v, want %v", c.env, got, c.want)
		}
	}
}

func TestNotifySequence(t *testing.T) {
	for _, c := range []struct {
		n           Notify
		title, body string
		tmux        bool
		want        string
	}{
		{NotifyOSC9, "#trout", "<bob> max: hi", false, "\x1b]9;#trout: <bob> max: hi\a"},
		{NotifyOSC9, "4", "x", false, "\x1b]9;trout: 4: x\a"},
		{NotifyOSC777, "#a;b", "<bob> one;two", false, "\x1b]777;notify;#a,b;<bob> one;two\a"},
		{NotifyOSC99, "bob", "hi", false,
			"\x1b]99;i=trout7:d=0;bob\x1b\\\x1b]99;i=trout7:p=body;hi\x1b\\"},
		{NotifyOSC777, "bob", "hi", true, "\x1bPtmux;\x1b\x1b]777;notify;bob;hi\a\x1b\\"},
		// Control characters can't end the sequence early; spaces collapse.
		{NotifyOSC777, "bob", "a\x07b\x1b]x\n\n  c\x02d", false, "\x1b]777;notify;bob;ab]x cd\a"},
		{NotifyNone, "bob", "hi", false, ""},
	} {
		if got := c.n.Sequence(7, c.title, c.body, c.tmux); got != c.want {
			t.Errorf("%v %q %q: %q, want %q", c.n, c.title, c.body, got, c.want)
		}
	}
	long := make([]rune, 300)
	for i := range long {
		long[i] = 'x'
	}
	if got := clean(string(long), 240); len([]rune(got)) != 241 || got[len(got)-3:] != "…" {
		t.Errorf("not cut at 240: %d runes", len([]rune(got)))
	}
}

func TestClipboardSequence(t *testing.T) {
	if got := ClipboardSequence("hi \x03"+"4red", false); got != "\x1b]52;c;aGkgAzRyZWQ=\a" {
		t.Errorf("got %q", got)
	}
	if got := ClipboardSequence("hi", true); got != "\x1b]52;c;aGk=\a\x1bPtmux;\x1b\x1b]52;c;aGk=\a\x1b\\" {
		t.Errorf("tmux: got %q", got)
	}
}
