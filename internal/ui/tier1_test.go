package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MaxRenaud/trout/internal/demo"
	"github.com/MaxRenaud/trout/internal/state"
)

// TestMIRCCommands sends the commands that used to go out broken.
func TestMIRCCommands(t *testing.T) {
	a, n, sent, feed := optionsApp(t)
	w := n.AddChannel(&state.Window{Kind: state.KChannel, Name: "#go", Joined: true})
	other := n.AddChannel(&state.Window{Kind: state.KChannel, Name: "#rust", Joined: true})
	q := a.query(n, "alice", "")
	a.activate(w)
	for _, c := range []struct{ typed, want string }{
		{"privmsg bob hello there", "PRIVMSG bob :hello there"},
		{"query -n carol hi you", "PRIVMSG carol :hi you"},
		{"msg -m dave a b", "PRIVMSG dave :a b"},
		{"say /not a command", "PRIVMSG #go :/not a command"},
		{"describe #rust waves", "PRIVMSG #rust :\x01ACTION waves\x01"},
		{"omsg ops only", "PRIVMSG @#go :ops only"},
		{"onotice #rust heads up", "NOTICE @#rust :heads up"},
		{"qmsg brb", "PRIVMSG alice brb"},
		{"ctcpreply bob version trout 1", "NOTICE bob :\x01VERSION trout 1\x01"},
		{"topic #rust new topic here", "TOPIC #rust :new topic here"},
		{"kick #rust troll go away", "KICK #rust troll :go away"},
		{"hop", "PART #go"},
	} {
		a.ircCommand(w, c.typed)
		expectSent(t, sent, c.want)
	}
	// The rest of /hop waits for the PART to come back: soju drops a JOIN
	// right behind it. Then it rejoins, with the key if there is one.
	for len(sent) > 0 {
		if got := <-sent; strings.HasPrefix(got, "JOIN") {
			t.Fatalf("/hop sent %q before its PART came back", got)
		}
	}
	feed(":max!m@h PART #go")
	expectSent(t, sent, "JOIN #go")
	if n.Find(state.KChannel, "#go") == nil {
		t.Fatal("/hop closed the window")
	}
	feed(":max!m@h JOIN #go")
	feed(":srv 324 max #go +nk sesame")
	a.ircCommand(w, "hop")
	feed(":max!m@h PART #go")
	expectSent(t, sent, "JOIN #go sesame")
	feed(":max!m@h JOIN #go")
	feed(":srv MODE #go -k sesame")
	feed(":max!m@h PART #go") // a plain part closes it
	if n.Find(state.KChannel, "#go") != nil {
		t.Fatal("a /part after /hop rejoined")
	}
	if w.Key != "" {
		t.Fatalf("-k left the key %q", w.Key)
	}
	a.ircCommand(other, "partall see you")
	expectSent(t, sent, "PART #rust :see you")
	feed(":bob!b@h INVITE max #secret")
	a.ircCommand(w, "join -i")
	expectSent(t, sent, "JOIN #secret")
	q.Lines = []state.Line{{Text: "x"}}
	a.ircCommand(w, "clear alice")
	if len(q.Lines) != 0 {
		t.Fatal("/clear alice didn't clear alice's window")
	}
	a.ircCommand(w, "dcc chat bob")
	if !strings.Contains(lastLine(w), "open port") {
		t.Fatalf("/dcc chat said %q", lastLine(w))
	}
}

func TestTier1Keys(t *testing.T) {
	a := New(Options{Demo: true})
	a.Update(tea.WindowSizeMsg{Width: 110, Height: 30})
	w := a.active
	a.View()
	a.Update(tea.KeyMsg{Type: tea.KeyCtrlPgUp})
	if w.Scroll != 1 {
		t.Fatalf("Ctrl+PgUp scrolled %d lines", w.Scroll)
	}
	// With the editbox empty, Ctrl+Home and Ctrl+End go to the top and
	// bottom of the scrollback, like mIRC's.
	a.Update(tea.KeyMsg{Type: tea.KeyCtrlHome})
	a.View()
	top := w.Scroll
	a.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	if a.View(); top <= 1 || w.Scroll != top {
		t.Fatalf("Ctrl+Home scrolled to %d, but the top is %d", top, w.Scroll)
	}
	a.Update(tea.KeyMsg{Type: tea.KeyCtrlEnd})
	if w.Scroll != 0 {
		t.Fatalf("Ctrl+End left the scroll at %d", w.Scroll)
	}
	a.Update(tea.KeyMsg{Type: tea.KeyCtrlPgUp})
	// Shift+Up/Down too, for keyboards without PgUp, while the editbox
	// has one line.
	a.Update(tea.KeyMsg{Type: tea.KeyShiftUp})
	if w.Scroll != 2 {
		t.Fatalf("Shift+Up scrolled to %d", w.Scroll)
	}
	a.Update(tea.KeyMsg{Type: tea.KeyShiftDown})
	if w.Scroll != 1 {
		t.Fatalf("Shift+Down scrolled to %d", w.Scroll)
	}
	// Alt+PgUp/PgDn and Alt+Home/End, for terminals that keep the Ctrl
	// ones (KiTTY); Alt+Home/End scroll with text in the editbox too.
	a.Update(tea.KeyMsg{Type: tea.KeyPgUp, Alt: true})
	if w.Scroll != 2 {
		t.Fatalf("Alt+PgUp scrolled to %d", w.Scroll)
	}
	a.Update(tea.KeyMsg{Type: tea.KeyPgDown, Alt: true})
	if w.Scroll != 1 {
		t.Fatalf("Alt+PgDn scrolled to %d", w.Scroll)
	}
	w.Edit.Buf, w.Edit.Pos = []rune("hello there  world"), 18
	a.Update(tea.KeyMsg{Type: tea.KeyHome, Alt: true})
	if a.View(); w.Scroll != top {
		t.Fatalf("Alt+Home scrolled to %d, want %d", w.Scroll, top)
	}
	a.Update(tea.KeyMsg{Type: tea.KeyEnd, Alt: true})
	if w.Scroll != 0 || w.Edit.Pos != 18 {
		t.Fatalf("Alt+End: scroll %d, cursor %d", w.Scroll, w.Edit.Pos)
	}
	// Ctrl+Left/Right move a word, like mIRC's editbox; with Shift they
	// select.
	a.Update(tea.KeyMsg{Type: tea.KeyCtrlLeft})
	a.Update(tea.KeyMsg{Type: tea.KeyCtrlLeft})
	if w.Edit.Pos != 6 {
		t.Fatalf("Ctrl+Left twice: cursor %d", w.Edit.Pos)
	}
	a.Update(tea.KeyMsg{Type: tea.KeyCtrlShiftRight})
	if got := w.Edit.SelText(); got != "there  " {
		t.Fatalf("Ctrl+Shift+Right selected %q", got)
	}
	a.Update(tea.KeyMsg{Type: tea.KeyCtrlRight})
	if w.Edit.Pos != 18 || w.Edit.Sel {
		t.Fatalf("Ctrl+Right: cursor %d", w.Edit.Pos)
	}
	w.Edit.Buf, w.Edit.Pos = nil, 0
	a.Update(tea.KeyMsg{Type: tea.KeyCtrlPgUp})
	a.Update(tea.KeyMsg{Type: tea.KeyCtrlE})
	if string(w.Edit.Buf) != "\x1e" {
		t.Fatalf("Ctrl+E typed %q, want strikethrough", string(w.Edit.Buf))
	}
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b"), Alt: true})
	if d, ok := a.dlg.(*abookBox); !ok || d.tab != tabAddress {
		t.Fatalf("Alt+B opened %T", a.dlg)
	}
	a.dlg = nil
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k"), Alt: true})
	if _, ok := a.dlg.(*colorsBox); !ok {
		t.Fatalf("Alt+K opened %T", a.dlg)
	}
	a.dlg = nil
	// The Scripts Editor at Aliases, Remote or Popups, like mIRC's.
	for _, c := range []struct {
		r   rune
		tab edTab
	}{{'d', edAliases}, {'r', edRemote}, {'p', edPopups}} {
		a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{c.r}, Alt: true})
		if ed, ok := a.dlg.(*scriptEditor); !ok || ed.tab != c.tab {
			t.Fatalf("Alt+%c opened %T", c.r, a.dlg)
		}
		a.dlg = nil
	}
}

func TestFormatting(t *testing.T) {
	rs := parseMIRC("\x1ecrossed\x1e \x0352x", Style{}, &classicTheme.Pal)
	if !rs[0].St.Strike || rs[len(rs)-1].St.FG != 0xff0000 {
		t.Fatal("strikethrough or extended color 52 (red) not applied")
	}
	for text, want := range map[string]string{
		"join #trout-dev, or (#go).": "#trout-dev #go",
		// WHOIS's channels: the status prefix isn't part of the link.
		"alice on @#go +#rust #vim ~&#local x@#not": "#go #rust #vim #local",
	} {
		rs = parseMIRC(text, Style{}, &classicTheme.Pal)
		markURLs(rs)
		var links []string
		for i := 0; i < len(rs); i++ {
			if rs[i].URL != nil && (i == 0 || rs[i-1].URL != rs[i].URL) {
				links = append(links, *rs[i].URL)
			}
		}
		if strings.Join(links, " ") != want {
			t.Errorf("%q: channel links %q, want %s", text, links, want)
		}
	}
}

func TestMessagesOptions(t *testing.T) {
	a, n, sent, feed := optionsApp(t)
	w := n.AddChannel(&state.Window{Kind: state.KChannel, Name: "#go", Joined: true})
	a.activate(w)

	a.keepOpenOnPart = true
	feed(":max!m@h PART #go")
	if n.Find(state.KChannel, "#go") == nil || w.Joined {
		t.Fatal("Keep channels open: the window closed, or still says joined")
	}

	a.ctcpActive = true
	feed(":bob!b@h PRIVMSG max :\x01TIME\x01")
	if !strings.Contains(strings.Join(lineTexts(w), "\n"), "[bob TIME]") {
		t.Fatal("CTCP requests didn't show in the active window")
	}

	a.away, a.awayCancel = "lunch", true
	w.Joined = true
	a.say(w, "back already")
	if a.away != "" {
		t.Fatal("talking didn't cancel away")
	}

	a.invisible = true
	feed(":srv 376 max :End of /MOTD command.")
	expectSent(t, sent, "MODE max +i")

	a.prefixOwn = false
	rows := a.lineRows(w, state.Line{Role: state.RoleOwn, Text: "<max> hello"}, 80, false)
	if text := runesText(rows[0].runes); strings.Contains(text, "<max>") {
		t.Fatalf("Prefix own messages off still shows %q", text)
	}
	a.stripCodes = true
	rows = a.lineRows(w, state.Line{Role: state.RoleNormal, Text: "<bob> \x0304red"}, 80, false)
	if rows[0].runes[len(rows[0].runes)-1].St.FG == classicTheme.Pal[4] {
		t.Fatal("Strip codes kept the color")
	}
}

func runesText(rs []SRune) string {
	var b strings.Builder
	for _, r := range rs {
		b.WriteRune(r.R)
	}
	return b.String()
}

func TestFavoritesPopAndAdd(t *testing.T) {
	a, n, _, feed := optionsApp(t)
	a.setFavorite(n, "#go", true)
	feed(":srv 376 max :End of /MOTD command.")
	l, ok := a.dlg.(*listBox)
	if !ok || !strings.HasPrefix(l.Title, "Favorites") {
		t.Fatalf("Favorites didn't pop up on connect: %T", a.dlg)
	}
	l.press(a, "Add...")
	f := a.dlg.(*formBox)
	f.Fields[0].Edit.Buf = []rune("rust")
	a.dlg.key(a, "enter")
	if got := strings.Join(a.favorites(n), " "); got != "#go #rust" {
		t.Fatalf("favorites %q", got)
	}
	a.dlg = nil
	feed(":srv 376 max :End of /MOTD command.")
	if a.dlg != nil {
		t.Fatal("Favorites popped up twice in a session")
	}
}

func TestChannelsListFilter(t *testing.T) {
	a, n, _, _ := optionsApp(t)
	a.activate(n.Status())
	a.chanLists[n] = &chanList{at: time.Now(), rows: []demo.ChanInfo{
		{Name: "#go", Users: 500, Topic: "the Go language"}, {Name: "#tiny", Users: 2}, {Name: "#golang-dev", Users: 40}}}
	a.openChannelsList(false)
	l := a.dlg.(*listBox)
	l.press(a, "Filter...")
	f := a.dlg.(*formBox)
	f.Fields[0].Edit.Buf = []rune("go")
	f.Fields[1].Edit.Buf = []rune("50")
	a.dlg.key(a, "enter")
	if len(l.Keys) != 1 || l.Keys[0] != "#go" || !strings.Contains(l.Title, "1 of 3") {
		t.Fatalf("filtered: %q %v", l.Title, l.Keys)
	}
}

func TestDCCFileExists(t *testing.T) {
	a, _, feed := realApp(t)
	a.downloadDir, a.dccWarn = t.TempDir(), false
	os.WriteFile(filepath.Join(a.downloadDir, "pack.bin"), make([]byte, 100), 0o644)
	feed(":bot!b@h PRIVMSG max :\x01DCC SEND pack.bin 3232235777 6800 1024\x01")
	d, ok := a.dlg.(*msgBox)
	if !ok || strings.Join(d.Buttons, " ") != "Resume Overwrite Rename Decline" {
		t.Fatalf("an existing partial file: %T %+v", a.dlg, a.dlg)
	}
	d.close(a, 2) // Rename
	tr := a.transfers[len(a.transfers)-1]
	if tr.path != filepath.Join(a.downloadDir, "pack (1).bin") || tr.from != 0 {
		t.Fatalf("Rename saves to %q from %d", tr.path, tr.from)
	}
	tr.cancel()
}

func TestOnlineTimer(t *testing.T) {
	a, n, _, _ := optionsApp(t)
	a.upAt[n] = time.Now().Add(-(90 * time.Minute))
	if title := a.winTitle(n.Status()); !strings.Contains(title, "[online 1:30]") {
		t.Fatalf("title %q", title)
	}
}

// TestIdleMotion moves the pointer over every cell: a motion that keeps
// the last frame (idleMotion) must leave the screen as a fresh one would
// draw it, hover highlights and all, and most motions keep it.
func TestIdleMotion(t *testing.T) {
	a := New(Options{Demo: true})
	a.Update(tea.WindowSizeMsg{Width: 110, Height: 30})
	a.View()
	kept := 0
	for y := range a.h {
		for x := range a.w {
			a.Update(tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionMotion, Button: tea.MouseButtonNone})
			if a.sameFrame {
				kept++
			}
			if got, want := a.View(), a.render(); got != want {
				t.Fatalf("at %d,%d the kept frame isn't what it would draw", x, y)
			}
		}
	}
	if kept < a.w*a.h/2 {
		t.Errorf("only %d of %d motions kept the frame", kept, a.w*a.h)
	}
}
