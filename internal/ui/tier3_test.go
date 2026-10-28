package ui

import (
	"bytes"
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MaxRenaud/trout/internal/config"
	"github.com/MaxRenaud/trout/internal/state"
	"github.com/MaxRenaud/trout/internal/termkeys"
)

func press(a *App, keys ...string) {
	for _, k := range keys {
		var m tea.KeyMsg
		switch k {
		case "enter":
			m = tea.KeyMsg{Type: tea.KeyEnter}
		case "alt+enter":
			m = tea.KeyMsg{Type: tea.KeyEnter, Alt: true}
		case "shift+left":
			m = tea.KeyMsg{Type: tea.KeyShiftLeft}
		case "shift+home":
			m = tea.KeyMsg{Type: tea.KeyShiftHome}
		case "ctrl+b":
			m = tea.KeyMsg{Type: tea.KeyCtrlB}
		case "ctrl+x":
			m = tea.KeyMsg{Type: tea.KeyCtrlX}
		case "up":
			m = tea.KeyMsg{Type: tea.KeyUp}
		case "tab":
			m = tea.KeyMsg{Type: tea.KeyTab}
		case "alt+q":
			m = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q"), Alt: true}
		default:
			m = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
		}
		a.Update(m)
	}
}

func TestWindowOptions(t *testing.T) {
	a, n, feed := realApp(t)
	ch := n.Find(state.KChannel, "#go")
	a.activate(ch)

	// Timestamps off for this window only.
	a.stamps = true
	items := a.winOptItems(ch)
	items[0].Action(a)
	if a.stampsFor(ch) || !a.stampsFor(n.Status()) || a.memBook.Windows["libera/#go"].Timestamps != "off" {
		t.Fatalf("Timestamp toggle: %+v", a.memBook.Windows)
	}
	a.winOptItems(ch)[0].Action(a) // back to the global setting: nothing saved
	if len(a.memBook.Windows) != 0 {
		t.Fatalf("back to default should forget the window: %+v", a.memBook.Windows)
	}

	// Events: hidden, then in the status window, where a quit shows once.
	a.setWinOpts(ch, func(o *config.WindowOpts) { o.Events = "hide" })
	before := len(ch.Lines)
	feed(":carol!c@h JOIN #go")
	if len(ch.Lines) != before || ch.NickIndex("carol") < 0 {
		t.Fatal("hidden join shown, or carol not in the nicklist")
	}
	a.setWinOpts(ch, func(o *config.WindowOpts) { o.Events = "status" })
	feed(":max!m@h JOIN #rust", ":srv 353 max = #rust :bob max", ":srv 366 max #rust :End")
	a.setWinOpts(n.Find(state.KChannel, "#rust"), func(o *config.WindowOpts) { o.Events = "status" })
	feed(":bob!b@h NICK bobby", ":bobby!b@h NICK bob")
	if got := n.Status().Lines; got[len(got)-2].Text != "* bob is now known as bobby" || got[len(got)-3].Text == got[len(got)-2].Text {
		t.Fatalf("a nick change in two channels shows once in status: %q / %q", got[len(got)-3].Text, got[len(got)-2].Text)
	}
	feed(":carol!c@h PART #go :bye", ":bob!b@h QUIT :gone")
	st := n.Status().Lines
	if len(ch.Lines) != before || !strings.HasSuffix(st[len(st)-2].Text, "(bye) (#go)") || st[len(st)-1].Text != "* Quits: bob (b@h) (gone)" {
		t.Fatalf("events in status: %q / %q", st[len(st)-2].Text, st[len(st)-1].Text)
	}

	// Beeping on every message, Track URLs off.
	a.focused = false
	a.setWinOpts(ch, func(o *config.WindowOpts) { o.Beep, o.TrackURLs = "on", "off" })
	feed(":alice!a@h PRIVMSG #go :see https://example.com")
	a.bellDue = false
	a.beepFor(ch, ch.Lines[len(ch.Lines)-1]) // Update rings and clears it
	if !a.bellDue {
		t.Fatal("the window's Beeping should beep on a plain message")
	}
	if len(a.urls) != 0 {
		t.Fatal("Track URLs off still tracked a link")
	}

	// Nicklist on the left.
	a.setWinOpts(ch, func(o *config.WindowOpts) { o.Nicklist = "left" })
	a.View()
	nickX := -1
	for _, r := range a.regions {
		if r.Kind == HitNick && r.S == "alice" {
			nickX = r.X
		}
	}
	if nickX < 0 || nickX > a.treeW+3 {
		t.Fatalf("nicklist should be on the left, alice at x=%d", nickX)
	}
}

func TestEditboxSelection(t *testing.T) {
	a, n, _ := realApp(t)
	a.activate(n.Find(state.KChannel, "#go"))
	e := a.edit(a.active)
	press(a, "h", "e", "l", "l", "o", "shift+left", "shift+left")
	if e.SelText() != "lo" {
		t.Fatalf("selected %q", e.SelText())
	}
	press(a, "ctrl+b")
	if string(e.Buf) != "hel\x02lo\x02" {
		t.Fatalf("Ctrl+B around the selection: %q", string(e.Buf))
	}
	press(a, "shift+home")
	press(a, "X")
	if string(e.Buf) != "X" {
		t.Fatalf("typing over a selection: %q", string(e.Buf))
	}
	press(a, "shift+left", "ctrl+x")
	if len(e.Buf) != 0 {
		t.Fatalf("Ctrl+X: %q", string(e.Buf))
	}

	var copied string
	write := writeClipboard
	writeClipboard = func(text string) { copied = text }
	t.Cleanup(func() { writeClipboard = write })
	press(a, "h", "i", "shift+left", "shift+left")
	if cmd := a.key(tea.KeyMsg{Type: tea.KeyCtrlC}); cmd != nil {
		cmd()
	}
	if copied != "hi" || e.Sel {
		t.Fatalf("Ctrl+C copied %q, selection left %v", copied, e.Sel)
	}
	// With nothing selected it doesn't quit, like mIRC.
	if cmd := a.key(tea.KeyMsg{Type: tea.KeyCtrlC}); cmd != nil || !strings.Contains(lastLine(a.active), "/exit quits") {
		t.Fatalf("Ctrl+C with nothing selected: %q", lastLine(a.active))
	}
}

func TestMultiLineEditbox(t *testing.T) {
	a, n, sent, feed := optionsApp(t)
	feed(":max!m@h JOIN #go")
	ch := n.Find(state.KChannel, "#go")
	a.activate(ch)
	press(a, "o", "n", "e", "alt+enter", "t", "w", "o")
	if string(ch.Edit.Buf) != "one\ntwo" || editHeight(&ch.Edit, 80) != 2 {
		t.Fatalf("multi-line: %q", string(ch.Edit.Buf))
	}
	press(a, "up", "X")
	if string(ch.Edit.Buf) != "oneX\ntwo" {
		t.Fatalf("Up moves a line in a multi-line box: %q", string(ch.Edit.Buf))
	}
	a.View()
	press(a, "enter")
	expectSent(t, sent, "PRIVMSG #go oneX")
	expectSent(t, sent, "PRIVMSG #go two")
}

func TestSecondEditbox(t *testing.T) {
	a, n, sent, feed := optionsApp(t)
	feed(":max!m@h JOIN #go")
	ch := n.Find(state.KChannel, "#go")
	a.activate(ch)
	press(a, "d", "r", "a", "f", "t", "alt+q", "h", "i")
	if !ch.Edit2On || string(ch.Edit.Buf) != "draft" || string(ch.Edit2.Buf) != "hi" {
		t.Fatalf("second editbox: %q / %q", string(ch.Edit.Buf), string(ch.Edit2.Buf))
	}
	a.View()
	press(a, "enter")
	expectSent(t, sent, "PRIVMSG #go hi")
	if string(ch.Edit.Buf) != "draft" {
		t.Fatal("the draft in the first editbox went away")
	}
	press(a, "alt+q")
	if ch.Edit2On || a.edit(ch) != &ch.Edit {
		t.Fatal("Alt+Q again should close it")
	}
}

func TestWildcardCompletion(t *testing.T) {
	a, n, _ := realApp(t)
	ch := n.Find(state.KChannel, "#go")
	ch.AddNick(state.Nick{Name: "[afk]bob"})
	a.activate(ch)
	press(a, "*", "b", "o", "b", "tab")
	if got := string(ch.Edit.Buf); got != "[afk]bob"+a.tabSuffix {
		t.Fatalf("*bob<Tab>: %q", got)
	}
	if !state.Wild("a*c?", "abbbcd") || state.Wild("a*c", "abd") || !state.Wild("[x]*", "[x]y") {
		t.Fatal("state.Wild")
	}
	if !wildMatch("[AFK]*!*@*", "[afk]bob!b@h") {
		t.Fatal("brackets in a mask are plain characters")
	}
}

func TestNickHotlinks(t *testing.T) {
	a, n, feed := realApp(t)
	ch := n.Find(state.KChannel, "#go")
	a.activate(ch)
	feed(":bob!b@h PRIVMSG #go :alice: see https://alice.example and ask nobody")
	a.View()
	var got []string
	for _, r := range a.regions {
		if r.Kind == HitNickText {
			got = append(got, r.S)
		}
	}
	// <bob> and "alice:" are nicks; the link and "nobody" aren't.
	if strings.Join(got, ",") != "bob,alice" {
		t.Fatalf("nick hotlinks: %v", got)
	}
}

func TestCopyModes(t *testing.T) {
	a, n, feed := realApp(t)
	ch := n.Find(state.KChannel, "#go")
	a.activate(ch)
	a.stamps = false
	feed(":bob!b@h PRIVMSG #go :\x02bold\x02 text")
	a.View()
	rows, _ := a.windowRows(ch, a.msgW)
	last := len(rows) - 1
	a.sel = &selection{win: ch, ar: last, ac: 0, br: last, bc: 30, moved: true, codes: true}
	if got := a.selectedText(); got != "<bob> \x02bold\x02 text" {
		t.Fatalf("Ctrl copy keeps codes: %q", got)
	}
	a.sel.codes = false
	if got := a.selectedText(); got != "<bob> bold text" {
		t.Fatalf("plain copy: %q", got)
	}
}

func TestWindowOrder(t *testing.T) {
	a, n, feed := realApp(t)
	feed(":max!m@h JOIN #rust")
	a.activate(n.Find(state.KChannel, "#rust"))
	a.openOrder()
	l := a.dlg.(*listBox)
	l.Sel = 1 // #rust
	l.press(a, "Move Up")
	if n.Wins[1].Name != "#rust" || n.Wins[2].Name != "#go" || l.Sel != 0 {
		t.Fatalf("order: %s %s", n.Wins[1].Name, n.Wins[2].Name)
	}
}

func TestDCCOptions(t *testing.T) {
	a, n, _ := realApp(t)
	dir := t.TempDir()
	a.downloadDir = dir
	a.dccWarn = false

	// mIRC's default file types are ignored.
	a.dccOffer(n, mustParse(t, ":eve!e@h PRIVMSG max :\x01DCC SEND setup.exe 2130706433 5000 10\x01"), "SEND setup.exe 2130706433 5000 10", time.Now())
	if !strings.Contains(lastLine(n.Status()), "Ignored setup.exe") || len(a.transfers) != 0 {
		t.Fatalf("an .exe offer: %q", lastLine(n.Status()))
	}
	a.dccIgnoreMode = 1 // accept only the list
	if !a.dccTypeIgnored("song.mp3") || a.dccTypeIgnored("x.exe") {
		t.Fatal("accept-only mode")
	}
	a.dccIgnoreMode = 0

	// A trusted sender's file is taken without asking, into its folder.
	music := filepath.Join(dir, "music")
	a.memBook.DCCTrusted = []string{"bot*"}
	a.memBook.DCCFolders = []config.DCCFolder{{Mask: "*.mp3", Dir: music}}
	a.dccOffer(n, mustParse(t, ":bot1!b@203.0.113.9 PRIVMSG max :x"), "SEND song.mp3 3405803785 5000 10", time.Now())
	if a.dlg != nil || len(a.transfers) != 1 || a.transfers[0].path != filepath.Join(music, "song.mp3") {
		t.Fatalf("trusted offer: dialog %T, transfers %d", a.dlg, len(a.transfers))
	}
	if _, err := os.Stat(music); err != nil {
		t.Fatal("the folder wasn't made")
	}
	a.cancelTransfer(a.transfers[0])

	// Someone else still asks.
	a.dccOffer(n, mustParse(t, ":eve!e@203.0.113.9 PRIVMSG max :x"), "SEND notes.txt 3405803785 5000 10", time.Now())
	if d, ok := a.dlg.(*msgBox); !ok || d.Title != "DCC Get" {
		t.Fatalf("untrusted offer: %T", a.dlg)
	}
}

func TestProxyOptions(t *testing.T) {
	a, _, _ := realApp(t)
	a.openProxy()
	f := a.dlg.(*formBox)
	f.toggle(f.Fields[2]) // SOCKS5
	if f.Fields[0].On || !f.Fields[2].On {
		t.Fatal("radio buttons")
	}
	f.Fields[4].Edit.Buf = []rune("127.0.0.1")
	f.Fields[5].Edit.Buf = []rune("1")
	f.Fields[7].Edit.Buf = []rune("pw")
	f.press(a, 0)
	p := a.memBook.Proxy
	if p.Type != "socks5" || p.Port != 1 || !p.Bouncer || !p.DCC || a.proxyPassword() != "pw" {
		t.Fatalf("proxy saved: %+v", p)
	}
	// Nothing listens on port 1, so going through the proxy fails, and
	// says it was the proxy.
	_, err := a.dialer(false)(context.Background(), "tcp", "example.com:6697")
	if err == nil || !strings.Contains(err.Error(), "proxy 127.0.0.1:1") {
		t.Fatalf("dial through the proxy: %v", err)
	}
	a.memBook.Proxy.DCC = false
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	defer ln.Close()
	if c, err := a.dialer(true)(context.Background(), "tcp", ln.Addr().String()); err != nil {
		t.Fatalf("DCC should connect directly: %v", err)
	} else {
		c.Close()
	}
}

func TestTier3Config(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	c, err := config.Load(path)
	if err != nil || len(c.DCCIgnore) != len(config.DefaultDCCIgnore) {
		t.Fatalf("a new config ignores mIRC's file types: %v %v", c.DCCIgnore, err)
	}
	c.DCCIgnore = []string{}
	c.Windows = map[string]config.WindowOpts{"libera/#go": {Events: "hide"}}
	c.Proxy = config.Proxy{Type: "socks5", Host: "localhost", Port: 1080, Bouncer: true}
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	c, err = config.Load(path)
	if err != nil || c.DCCIgnore == nil || len(c.DCCIgnore) != 0 || c.Windows["libera/#go"].Events != "hide" || c.Proxy.Host != "localhost" {
		t.Fatalf("round trip: %+v %v", c, err)
	}
}

func TestSelfCTCP(t *testing.T) {
	a, n, _, feed := optionsApp(t)
	st := n.Status()
	// The echo and the delivery of a CTCP to yourself share a msgid.
	feed("@msgid=1 :max!m@h PRIVMSG max :\x01VERSION\x01", "@msgid=1 :max!m@h PRIVMSG max :\x01VERSION\x01")
	asked := 0
	for _, l := range st.Lines {
		if l.Text == "[max VERSION]" {
			asked++
		}
	}
	if asked != 1 || len(a.ctcpSent) != 1 {
		t.Fatalf("a CTCP to yourself: shown %d times, answered %d", asked, len(a.ctcpSent))
	}
	// Your own CTCP to someone else, echoed, isn't a request.
	feed("@msgid=2 :max!m@h PRIVMSG bob :\x01VERSION\x01")
	if lastLine(st) == "[max VERSION]" && len(a.ctcpSent) != 1 {
		t.Fatal("answered our own echo")
	}
	// A request the bouncer replays from history isn't answered.
	feed("@time=2020-01-01T00:00:00.000Z :bob!b@h PRIVMSG max :\x01TIME\x01")
	if len(a.ctcpSent) != 1 {
		t.Fatal("answered a CTCP from history")
	}
}

func TestOptionKey(t *testing.T) {
	a, _, _ := realApp(t)
	var out bytes.Buffer
	a.bellOut, a.termHome = &out, t.TempDir()
	a.term = termkeys.Terminal{Name: "iTerm2", Fix: termkeys.FixITermProfile, Profile: "Default"}

	// Asked at startup; "Not Now" asks again next time, unless checked.
	a.optionKeyStart()
	d, ok := a.dlg.(*msgBox)
	if !ok || d.Title != "Option Key" || d.Buttons[0] != "Fix It" {
		t.Fatalf("startup dialog: %T", a.dlg)
	}
	d.OnClose(a, 1)
	if a.optionKey != "" {
		t.Fatal("Not Now shouldn't stop the reminder")
	}
	a.optionKeyStart()
	d = a.dlg.(*msgBox)
	d.Checked = true
	d.OnClose(a, 1)
	a.dlg = nil
	if a.optionKey != "never" || a.settings().OptionKey != "never" {
		t.Fatal("Don't remind me again")
	}
	if a.optionKeyStart(); a.dlg != nil {
		t.Fatal("asked again after never")
	}

	// Fix It: the dynamic profile, the switch, and the way back.
	a.optionKey = ""
	a.openOptionKey()
	a.dlg.(*msgBox).OnClose(a, 0)
	if a.optionKey != "fix" || a.itermRestore != "\x1b]1337;SetProfile=Default\x07" {
		t.Fatalf("Fix It: %q %q", a.optionKey, a.itermRestore)
	}
	if _, err := os.Stat(filepath.Join(a.termHome, "Library/Application Support/iTerm2/DynamicProfiles/trout.json")); err != nil {
		t.Fatal("no dynamic profile")
	}
	a.optionKeyStart()() // the next start: the profile is there, so it switches right away
	if out.String() != "\x1b]1337;SetProfile=trout (Default)\x07" {
		t.Fatalf("switch: %q", out.String())
	}
	out.Reset()
	a.stopITermProfile()
	if out.String() != "\x1b]1337;SetProfile=Default\x07" || a.itermRestore != "" {
		t.Fatalf("switch back: %q", out.String())
	}

	// kitty and Ghostty get a line in their config.
	conf := filepath.Join(t.TempDir(), "ghostty", "config")
	a.term = termkeys.Terminal{Name: "Ghostty", Fix: termkeys.FixConfigLine, Config: conf, Line: "macos-option-as-alt = left"}
	a.openOptionKey()
	a.dlg.(*msgBox).OnClose(a, 0)
	if b, _ := os.ReadFile(conf); !strings.Contains(string(b), "macos-option-as-alt = left") {
		t.Fatalf("Add It: %q", b)
	}
}

// TestSurrogates: on Windows an emoji arrives as two UTF-16 halves, one key
// each; they're put back together, and a lone half is dropped.
func TestSurrogates(t *testing.T) {
	a, n, _ := realApp(t)
	a.activate(n.Find(state.KChannel, "#go"))
	e := a.edit(a.active)
	for _, r := range []rune{'f', 0xD83D, 0xDC1F, ' ', 0xD83D, 'x', 0xDC1F} {
		a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if got := string(e.Buf); got != "f🐟 x" {
		t.Fatalf("got %q", got)
	}
	e.Buf, e.Pos = nil, 0
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a', 0xD83D, 0xDC1F, 'b'}, Paste: true}) // pasted at once
	if got := string(e.Buf); got != "a🐟b" {
		t.Fatalf("pasted: %q", got)
	}
}

// TestControlRunes: a NUL from the Windows console (or any control
// character but line breaks, tabs and format codes) doesn't get into the
// editbox, where it would make the message impossible to send; one between
// an emoji's halves doesn't keep them apart.
func TestControlRunes(t *testing.T) {
	a, n, _ := realApp(t)
	a.activate(n.Find(state.KChannel, "#go"))
	e := a.edit(a.active)
	for _, r := range []rune{'a', 0, 0xD83D, 0, 0xDC1F, 0x07, 0x02, 'b', 0x7f} {
		a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if got := string(e.Buf); got != "a🐟\x02b" {
		t.Fatalf("got %q", got)
	}
}
