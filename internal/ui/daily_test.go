package ui

import (
	"bytes"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ergochat/irc-go/ircmsg"
	"github.com/mattn/go-runewidth"

	"github.com/MaxRenaud/trout/internal/config"
	"github.com/MaxRenaud/trout/internal/irc"
	"github.com/MaxRenaud/trout/internal/script"
	"github.com/MaxRenaud/trout/internal/state"
	"github.com/MaxRenaud/trout/internal/termkeys"
)

// realApp is a real-mode App with one network and a #go channel, fed raw
// lines with feed. Nothing is connected.
func realApp(t *testing.T) (*App, *state.Network, func(lines ...string)) {
	a := New(Options{Bouncer: config.Bouncer{Address: "irc+insecure://127.0.0.1:6667", User: "max"}})
	a.Update(tea.WindowSizeMsg{Width: 110, Height: 30})
	n := state.NewNetwork("Libera")
	n.ID, n.Nick = "1", "max"
	a.nets = append(a.nets, n)
	feed := func(lines ...string) {
		t.Helper()
		for _, l := range lines {
			m, err := ircmsg.ParseLine(l)
			if err != nil {
				t.Fatalf("parsing %q: %v", l, err)
			}
			a.Update(ircLineMsg{n, m})
		}
	}
	feed(":max!m@h JOIN #go", ":srv 353 max = #go :@alice bob max", ":srv 366 max #go :End")
	return a, n, feed
}

func lastLine(w *state.Window) string { return w.Lines[len(w.Lines)-1].Text }

func TestWhois(t *testing.T) {
	a, n, feed := realApp(t)
	ch := n.Find(state.KChannel, "#go")
	a.activate(ch)
	feed(
		":srv 311 max alice ~a example.net * :Alice Liddell",
		":srv 319 max alice :@#go #trout",
		":srv 312 max alice irc.libera.chat :Libera server",
		":srv 301 max alice :gone fishing",
		":srv 330 max alice alice :is logged in as",
		":srv 317 max alice 3723 1793145600 :seconds idle, signon time",
		":srv 318 max alice :End of /WHOIS list.",
	)
	var got []string
	for _, l := range ch.Lines[len(ch.Lines)-7:] {
		got = append(got, l.Text)
	}
	want := []string{
		"alice is ~a@example.net * Alice Liddell",
		"alice on @#go #trout",
		"alice using irc.libera.chat Libera server",
		"alice is away: gone fishing",
		"alice is logged in as alice",
		"alice has been idle 1hr 2mins 3secs, signed on ", // then the local time
		"alice End of /WHOIS list.",
	}
	for i := range want {
		if i == 5 {
			if !strings.HasPrefix(got[i], "alice has been idle 1hr 2mins 3secs, signed on ") {
				t.Errorf("line %d = %q", i, got[i])
			}
			continue
		}
		if got[i] != want[i] {
			t.Errorf("line %d = %q, want %q", i, got[i], want[i])
		}
	}
	if len(n.Status().Lines) > 0 && strings.Contains(lastLine(n.Status()), "alice") {
		t.Error("WHOIS went to the status window instead of the window you're in")
	}
}

func TestAway(t *testing.T) {
	a, n, feed := realApp(t)
	ch := n.Find(state.KChannel, "#go")
	a.quietWho[whoKey(n, "#go")] = true
	statusBefore := len(n.Status().Lines)
	feed(":srv 352 max #go ~b host srv bob G :0 Bob",
		":srv 352 max #go ~a host srv alice H@ :0 Alice",
		":srv 315 max #go :End of /WHO list.")
	if len(n.Status().Lines) != statusBefore {
		t.Fatal("trout's own WHO printed in the status window")
	}
	away := func(nick string) bool { return ch.Nicks[ch.NickIndex(nick)].Away }
	if !away("bob") || away("alice") {
		t.Fatalf("after WHO: bob away %v, alice away %v", away("bob"), away("alice"))
	}

	q := a.query(n, "alice", "")
	feed(":alice!a@h AWAY :lunch")
	if !away("alice") || lastLine(q) != "* alice is away: lunch" {
		t.Fatalf("away-notify: alice away %v, query says %q", away("alice"), lastLine(q))
	}
	feed(":alice!a@h AWAY")
	if away("alice") || lastLine(q) != "* alice is back" {
		t.Fatalf("back: alice away %v, query says %q", away("alice"), lastLine(q))
	}

	a.ircCommand(ch, "away gone fishing")
	if a.away != "gone fishing" || !strings.Contains(a.winTitle(n.Status()), "[away]") {
		t.Error("the status title doesn't say you're away")
	}
}

func TestBell(t *testing.T) {
	a, n, _ := realApp(t)
	var out bytes.Buffer
	a.bellOut = &out
	run := func(cmd tea.Cmd) {
		if cmd == nil {
			return
		}
		if b, ok := cmd().(tea.BatchMsg); ok {
			for _, c := range b {
				if c != nil {
					c()
				}
			}
		}
	}
	ch := n.Find(state.KChannel, "#go")
	a.activate(ch)
	m, _ := ircmsg.ParseLine(":bob!b@h PRIVMSG #go :max: look")
	_, cmd := a.Update(ircLineMsg{n, m})
	run(cmd)
	if out.Len() != 0 {
		t.Fatal("beeped for a highlight in the window you're looking at")
	}
	m, _ = ircmsg.ParseLine(":carol!c@h PRIVMSG max :psst")
	_, cmd = a.Update(ircLineMsg{n, m})
	run(cmd)
	if out.String() != "\a" {
		t.Fatalf("a private message in the background wrote %q, want a bell", out.String())
	}
}

// Tips are the terminal's notifications, mIRC's tips: for highlights and
// private messages while the terminal isn't in front.
func TestTips(t *testing.T) {
	a, n, _ := realApp(t)
	var out bytes.Buffer
	a.bellOut, a.beep, a.notify = &out, false, termkeys.NotifyOSC777
	if !a.tips {
		t.Fatal("tips are off by default")
	}
	say := func(line string) string {
		t.Helper()
		out.Reset()
		m, _ := ircmsg.ParseLine(line)
		_, cmd := a.Update(ircLineMsg{n, m})
		if cmd != nil {
			if b, ok := cmd().(tea.BatchMsg); ok {
				for _, c := range b {
					if c != nil {
						c()
					}
				}
			}
		}
		return out.String()
	}
	a.activate(n.Find(state.KChannel, "#go"))
	if got := say(":bob!b@h PRIVMSG #go :max: look"); got != "" {
		t.Fatalf("a tip while the terminal is in front: %q", got)
	}
	a.Update(tea.BlurMsg{})
	if got, want := say(":bob!b@h PRIVMSG #go :max: \x02look\x02 here"), "\x1b]777;notify;#go;<bob> max: look here\a"; got != want {
		t.Fatalf("highlight: %q, want %q", got, want)
	}
	if got := say(":bob!b@h PRIVMSG #go :max: again"); got != "" {
		t.Fatalf("a second tip from the same window straight away: %q", got)
	}
	if got, want := say(":carol!c@h PRIVMSG max :psst"), "\x1b]777;notify;carol;<carol> psst\a"; got != want {
		t.Fatalf("private message: %q, want %q", got, want)
	}
	if got := say(":dave!d@h PRIVMSG #go :hello all"); got != "" {
		t.Fatalf("a tip for a plain channel message: %q", got)
	}
	a.command(n.Status(), "tips off")
	if a.tips || lastLine(n.Status()) != "* Tips are off" {
		t.Fatalf("/tips off: tips %v, status says %q", a.tips, lastLine(n.Status()))
	}
	if got := say(":erin!e@h PRIVMSG max :hey"); got != "" {
		t.Fatalf("a tip with tips off: %q", got)
	}
	a.tips, a.notify = true, termkeys.NotifyNone
	if got := say(":frank!f@h PRIVMSG max :hey"); got != "" {
		t.Fatalf("a tip in a terminal that can't show one: %q", got)
	}
}

func TestFind(t *testing.T) {
	a, n, feed := realApp(t)
	ch := n.Find(state.KChannel, "#go")
	a.activate(ch)
	feed(":bob!b@h PRIVMSG #go :the trout is here", ":bob!b@h PRIVMSG #go :nothing",
		":bob!b@h PRIVMSG #go :another TROUT")
	a.View()
	a.Update(tea.KeyMsg{Type: tea.KeyCtrlF})
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("trout")})
	a.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if a.dlg != nil || a.find == nil || ch.Lines[a.find.line].Text != "<bob> another TROUT" {
		t.Fatal("Find didn't land on the newest match")
	}
	a.Update(tea.KeyMsg{Type: tea.KeyF3})
	if ch.Lines[a.find.line].Text != "<bob> the trout is here" {
		t.Fatal("F3 didn't go to the older match")
	}
	a.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if a.find != nil {
		t.Fatal("Esc didn't clear the match")
	}
}

// TestFindText: /findtext goes straight to the match above, then the
// next one up, with no dialog; -n goes down, like mIRC's.
func TestFindText(t *testing.T) {
	a, n, feed := realApp(t)
	ch := n.Find(state.KChannel, "#go")
	a.activate(ch)
	feed(":bob!b@h PRIVMSG #go :the trout is here", ":bob!b@h PRIVMSG #go :nothing",
		":bob!b@h PRIVMSG #go :another TROUT")
	a.View()
	typed := func(text string) {
		t.Helper()
		a.edit(ch).Buf = []rune(text)
		a.submit(ch)
	}
	at := func() string {
		if a.find == nil {
			return ""
		}
		return ch.Lines[a.find.line].Text
	}
	typed("/findtext trout")
	if a.dlg != nil || at() != "<bob> another TROUT" {
		t.Fatalf("/findtext: dialog %T, at %q", a.dlg, at())
	}
	typed("/findtext trout")
	if at() != "<bob> the trout is here" {
		t.Fatalf("again: at %q", at())
	}
	typed("/findtext -n trout")
	if at() != "<bob> another TROUT" {
		t.Fatalf("-n: at %q", at())
	}
	typed("/findtext salmon")
	if a.find != nil || !strings.Contains(ch.Lines[len(ch.Lines)-1].Text, `can't find "salmon" above`) {
		t.Fatalf("not found: %q", ch.Lines[len(ch.Lines)-1].Text)
	}
	typed("/findtext")
	if _, ok := a.dlg.(*formBox); !ok {
		t.Fatalf("/findtext alone opened %T", a.dlg)
	}
}

// TestQueryN: /query -n opens a query without bringing it to the front,
// and /queryrn renames one, with mIRC's errors (checked).
func TestQueryN(t *testing.T) {
	a, n, _ := realApp(t)
	ch := n.Find(state.KChannel, "#go")
	a.activate(ch)
	typed := func(text string) {
		t.Helper()
		a.edit(a.active).Buf = []rune(text)
		a.submit(a.active)
	}
	typed("/query -n bob")
	q := n.Find(state.KQuery, "bob")
	if q == nil || a.active != ch {
		t.Fatalf("/query -n: query %v, active %q", q, a.active.Name)
	}
	typed("/query bob")
	if a.active != q {
		t.Fatalf("/query: active %q", a.active.Name)
	}
	a.activate(ch)
	typed("/queryrn BOB robert")
	if q.Name != "robert" || n.Find(state.KQuery, "bob") != nil {
		t.Fatalf("/queryrn: %q", q.Name)
	}
	typed("/queryrn bob x")
	if got := lastLine(ch); got != "* /queryrn: invalid window" {
		t.Fatalf("/queryrn of none: %q", got)
	}
	n.Add(&state.Window{Kind: state.KQuery, Name: "ann"})
	typed("/queryrn robert ann")
	if got := lastLine(ch); got != "* /queryrn: invalid parameters" || q.Name != "robert" {
		t.Fatalf("/queryrn onto another: %q", got)
	}
}

func TestFavorites(t *testing.T) {
	cfg, _ := config.Load(filepath.Join(t.TempDir(), "config.toml"))
	a := New(Options{Config: cfg, Bouncer: config.Bouncer{Address: "irc+insecure://x:1", User: "max"}})
	n := state.NewNetwork("Libera")
	a.nets = append(a.nets, n)
	a.setFavorite(n, "#trout", true)
	a.setFavorite(n, "#go", true)
	saved, _ := config.Load(cfg.Path)
	if got := strings.Join(saved.Favorites["Libera"], " "); got != "#go #trout" {
		t.Fatalf("saved favorites %q", got)
	}
	a.activate(n.Status())
	a.openFavorites()
	a.dlg.key(a, "delete")
	saved, _ = config.Load(cfg.Path)
	if got := strings.Join(saved.Favorites["Libera"], " "); got != "#trout" {
		t.Fatalf("after Remove, favorites %q", got)
	}
}

func TestTrustNewKey(t *testing.T) {
	cfg, _ := config.Load(filepath.Join(t.TempDir(), "config.toml"))
	b := config.Bouncer{Name: "bnc", Address: "ircs://bnc.example.net", User: "max", Pin: "SHA256:old"}
	cfg.Use(b)
	a := New(Options{Config: cfg, Bouncer: b})
	a.Update(ircDownMsg{a.home, &irc.CertError{Addr: "bnc.example.net:6697", Fingerprint: "SHA256:new", Pinned: "SHA256:old"}})
	a.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if a.bnc.Pin != "SHA256:old" {
		t.Fatal("Enter trusted a changed key")
	}
	a.Update(ircDownMsg{a.home, &irc.CertError{Addr: "bnc.example.net:6697", Fingerprint: "SHA256:new", Pinned: "SHA256:old"}})
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("t")})
	if saved, _ := config.Load(cfg.Path); a.bnc.Pin != "SHA256:new" || saved.Bouncers[0].Pin != "SHA256:new" {
		t.Fatalf("Trust New Key: pin %q", a.bnc.Pin)
	}
}

// TestTabClose checks the switchbar's close boxes: on the active button,
// on the one under the mouse (not on status windows), and that clicking
// one closes that window.
func TestTabClose(t *testing.T) {
	a := New(Options{Demo: true})
	a.Update(tea.WindowSizeMsg{Width: 110, Height: 30})
	a.View()
	region := func(kind HitKind, name string) (Region, bool) {
		for _, r := range a.regions {
			if r.Kind == kind && r.Win != nil && r.Win.Name == name {
				return r, true
			}
		}
		return Region{}, false
	}
	if _, ok := region(HitChildBtn, a.active.Name); !ok {
		t.Fatalf("the active window %s has no close box", a.active.Name)
	}
	if _, ok := region(HitChildBtn, "alice"); ok {
		t.Fatal("a background button has a close box without the mouse on it")
	}
	tab, _ := region(HitSwitch, "alice")
	a.Update(tea.MouseMsg{X: tab.X + 1, Y: tab.Y, Action: tea.MouseActionMotion})
	a.View()
	x, ok := region(HitChildBtn, "alice")
	if !ok {
		t.Fatal("hovering alice's button didn't show its close box")
	}
	a.Update(tea.MouseMsg{X: x.X, Y: x.Y, Action: tea.MouseActionMotion}) // onto the ×
	a.View()
	if x2, ok := region(HitChildBtn, "alice"); !ok || x2 != x {
		t.Fatal("moving onto the × moved or hid it")
	}
	a.Update(tea.MouseMsg{X: x.X, Y: x.Y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if a.nets[0].Find(state.KQuery, "alice") != nil {
		t.Fatal("clicking the × didn't close alice's window")
	}
	status, _ := region(HitSwitch, a.nets[0].Name)
	a.Update(tea.MouseMsg{X: status.X + 1, Y: status.Y, Action: tea.MouseActionMotion})
	a.View()
	if _, ok := region(HitChildBtn, a.nets[0].Name); ok {
		t.Fatal("a status window's button got a close box")
	}
}

// TestCrowdedSwitchbar gives the switchbar more windows than fit: labels
// get shorter, then it wraps onto more rows, and every window keeps a
// button, like mIRC's.
func TestCrowdedSwitchbar(t *testing.T) {
	a := New(Options{Demo: true})
	a.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	n := a.nets[0]
	for i := range 30 {
		a.query(n, "somebody_with_a_long_nick_"+string(rune('a'+i%26))+strings.Repeat("x", i/26), "")
	}
	a.View()
	buttons := map[*state.Window]bool{}
	rows := map[int]bool{}
	for _, r := range a.regions {
		if r.Kind == HitSwitch {
			buttons[r.Win] = true
			rows[r.Y] = true
		}
	}
	if len(buttons) != len(a.order()) || len(rows) < 2 {
		t.Fatalf("%d of %d windows have buttons, on %d rows", len(buttons), len(a.order()), len(rows))
	}
	for i, line := range strings.Split(a.View(), "\n") {
		if w := runewidth.StringWidth(sgrRe.ReplaceAllString(line, "")); w != a.w {
			t.Fatalf("line %d is %d cells wide", i, w)
		}
	}
}

func TestNoticesInActive(t *testing.T) {
	a, n, feed := realApp(t)
	ch := n.Find(state.KChannel, "#go")
	a.activate(ch)
	a.query(n, "nickserv", "")
	feed(":NickServ!s@services. NOTICE max :This nickname is registered")
	if lastLine(ch) != "-NickServ- This nickname is registered" {
		t.Fatalf("a private notice went elsewhere; #go says %q", lastLine(ch))
	}
	feed(":irc.example.net NOTICE max :*** Looking up your hostname")
	if lastLine(n.Status()) != "-irc.example.net- *** Looking up your hostname" {
		t.Fatalf("a server notice: status says %q", lastLine(n.Status()))
	}
	feed(":max!m@h NOTICE bob :sent from my phone")
	if lastLine(ch) != "-> -bob- sent from my phone" {
		t.Fatalf("your own notice showed as %q", lastLine(ch))
	}
}

// TestTreeLongPress opens a window's popup by holding the left button on
// its treebar entry, like right-clicking it.
func TestTreeLongPress(t *testing.T) {
	a := New(Options{Demo: true})
	a.Update(tea.WindowSizeMsg{Width: 110, Height: 30})
	a.View()
	for _, r := range a.regions {
		if r.Kind == HitTree && r.Win != a.active {
			a.Update(tea.MouseMsg{X: r.X + 3, Y: r.Y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
			a.Update(holdMsg{a.holdSeq})
			if len(a.menus) != 1 || a.active != r.Win {
				t.Fatalf("holding %s: %d menus open, active %s", r.Win.Name, len(a.menus), a.active.Name)
			}
			return
		}
	}
	t.Fatal("no treebar entries")
}

// TestSwitchbarHover hovers every switchbar button at many widths and
// window counts: no button may vanish or run off the screen.
func TestSwitchbarHover(t *testing.T) {
	for width := 60; width <= 120; width += 7 {
		for extra := range 8 {
			a := New(Options{Demo: true})
			a.Update(tea.WindowSizeMsg{Width: width, Height: 30})
			for i := range extra {
				a.query(a.nets[0], "nick"+strings.Repeat("x", i), "")
			}
			a.View()
			var spots []Region
			for _, r := range a.regions {
				if r.Kind == HitSwitch {
					spots = append(spots, r)
				}
			}
			for _, s := range spots {
				a.Update(tea.MouseMsg{X: s.X + s.W - 1, Y: s.Y, Action: tea.MouseActionMotion})
				a.View()
				count := 0
				for _, r := range a.regions {
					if r.Kind == HitSwitch {
						count++
						if r.X+r.W > a.w {
							t.Fatalf("width %d: %s's button runs off the screen", width, r.Win.Name)
						}
					}
				}
				if count != len(a.order()) {
					t.Fatalf("width %d, %d windows: hovering %s left %d buttons", width, len(a.order()), s.Win.Name, count)
				}
			}
		}
	}
}

// TestButtonMnemonics: a dialog's buttons each underline a different
// letter (OK, Cancel and Close none, like mIRC's: they're Enter and Esc),
// and Alt and that letter presses the button, or the letter alone when no
// text field has the focus. The Scripts Editor's menus keep their letters.
func TestButtonMnemonics(t *testing.T) {
	letters := func(labels []string, taken string) string {
		var b strings.Builder
		for i, mn := range mnemonics(labels, taken) {
			if i > 0 {
				b.WriteByte(' ')
			}
			if mn < 0 {
				b.WriteByte('-')
			} else {
				b.WriteRune([]rune(labels[i])[mn])
			}
		}
		return b.String()
	}
	for _, c := range []struct {
		labels []string
		taken  string
		want   string
	}{
		{[]string{"Connect", "Cancel"}, "", "C -"},
		{serverButtons, "", "C D A E l -"},
		{[]string{"Move Up", "Move Down", "Close"}, "", "M D -"},
		{[]string{"Use", "Add", "Edit", "Update", "Remove", "Close"}, "", "U A E p R -"},
		{[]string{"Yes", "No", "Cancel"}, "", "Y N -"},
		{[]string{"OK", "Bans...", "Topics...", "Cancel"}, "", "- B T -"},
		{edButtons, edMnemonics, "- - p"},
	} {
		if got := letters(c.labels, c.taken); got != c.want {
			t.Errorf("%q: %s, want %s", c.labels, got, c.want)
		}
	}

	a, _, _ := realApp(t)
	alt := func(r rune) { a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}, Alt: true}) }
	plain := func(r rune) { a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}) }

	// A form: the letter alone types into the field, Alt and it presses.
	pressed := ""
	f := &formBox{Title: "Test", Fields: []*formField{newField("Name:", "", false)}, Buttons: []string{"OK", "Whois", "Cancel"},
		OnButton: map[string]func(*App) tea.Cmd{"Whois": func(*App) tea.Cmd { pressed = "Whois"; return nil }}}
	a.openDialog(f)
	plain('w')
	if pressed != "" || string(f.Fields[0].Edit.Buf) != "w" {
		t.Fatalf("w in a field pressed %q, field %q", pressed, string(f.Fields[0].Edit.Buf))
	}
	alt('w')
	if pressed != "Whois" {
		t.Fatal("Alt+W didn't press Whois")
	}
	a.dlg = nil

	// The Servers dialog has no fields: A alone is Add..., Alt+L Delete.
	a.openServers()
	plain('a')
	if l, ok := a.dlg.(*listBox); !ok || l.Title != "Add Network" {
		t.Fatalf("A in Servers opened %T, not Add Network", a.dlg)
	}
	a.dlg = nil
	a.openServers()
	alt('l')
	if d, ok := a.dlg.(*msgBox); !ok || d.Title != "Delete Network" {
		t.Fatalf("Alt+L in Servers opened %T, not Delete Network", a.dlg)
	}
	a.dlg = nil

	// The Scripts Editor: Alt+O is still its Options menu, Alt+P its Help
	// button.
	a.openScripts()
	alt('o')
	if len(a.menus) == 0 {
		t.Fatal("Alt+O in the Scripts Editor didn't open Options")
	}
	a.closeMenus()
	if _, ok := a.dlg.(*scriptEditor); !ok {
		t.Fatalf("the Scripts Editor closed: %T", a.dlg)
	}
}

// TestScriptsEditorButtons reaches the Scripts Editor's buttons from the
// keyboard: Tab indents the text, Shift+Tab goes to OK, Tab along to
// Cancel and back round to the text, and Enter presses.
func TestScriptsEditorButtons(t *testing.T) {
	a, _, _ := realApp(t)
	key := func(k tea.KeyType) { a.Update(tea.KeyMsg{Type: k}) }
	typ := func(s string) { a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}) }
	a.openScripts()
	ed := a.dlg.(*scriptEditor)
	typ("alias hi echo -a hi")
	key(tea.KeyTab)
	if got := ed.doc().data(); !strings.Contains(got, "echo -a hi  ") {
		t.Fatalf("Tab didn't indent: %q", got)
	}
	key(tea.KeyShiftTab)
	if ed.focus != 1 {
		t.Fatalf("Shift+Tab: focus %d, want OK", ed.focus)
	}
	typ("x") // a letter doesn't type while on the buttons
	key(tea.KeyTab)
	key(tea.KeyTab)
	key(tea.KeyTab)
	if ed.focus != 0 || strings.Contains(ed.doc().data(), "x") {
		t.Fatalf("Tab round the buttons: focus %d, text %q", ed.focus, ed.doc().data())
	}
	key(tea.KeyShiftTab)
	key(tea.KeyEnter) // OK: saved and closed
	if a.dlg != nil {
		t.Fatalf("Enter on OK left %T open", a.dlg)
	}
	if data, _ := a.readScript("remote.ini"); !strings.Contains(data, "alias hi") {
		t.Fatalf("OK didn't save remote.ini: %q", data)
	}

	a.openScripts()
	typ("more")
	key(tea.KeyShiftTab)
	key(tea.KeyRight) // Cancel
	key(tea.KeyEnter)
	if m, ok := a.dlg.(*msgBox); !ok || m.Lines[0] != "Save the changes to your scripts?" {
		t.Fatalf("Enter on Cancel opened %T, not the save prompt", a.dlg)
	}
	typ("n")
	if a.dlg != nil {
		t.Fatalf("N left %T open", a.dlg)
	}
}

// TestDisconnected: losing the connection says so in every channel, and
// typing there says it isn't connected instead of the text vanishing.
func TestDisconnected(t *testing.T) {
	a, n, _ := realApp(t)
	ch := n.Find(state.KChannel, "#go")
	a.activate(ch)
	a.Update(ircDownMsg{net: n, err: errors.New("i/o timeout")})
	if got := lastLine(ch); got != "* Disconnected" || ch.Joined {
		t.Fatalf("after the drop: %q", got)
	}
	a.edit(ch).Buf = []rune("test")
	a.submit(ch)
	if got := lastLine(ch); got != "* Not connected to Libera" {
		t.Fatalf("typing while disconnected: %q", got)
	}
}

// TestEchoActivity: a script's echo marks its window like an event (the
// join color), unless it's echo -m, like mIRC's.
func TestEchoActivity(t *testing.T) {
	a, n, _ := realApp(t)
	ch := n.Find(state.KChannel, "#go")
	a.activate(n.Status())
	ch.Act = state.ActNone
	a.runScript([]script.Action{{Echo: &script.Echo{Network: n.Name, Window: "#go", Color: -1, Text: "AS13335 Cloudflare"}}})
	if ch.Act != state.ActEvent {
		t.Errorf("echo: activity %d, want an event's", ch.Act)
	}
	// soju syncing a read marker works the activity out again from the
	// unread lines: the echo is still an event.
	a.conns[n] = &irc.Conn{Caps: map[string]bool{"draft/read-marker": true}}
	a.applyRead(ch)
	if ch.Act != state.ActEvent {
		t.Errorf("echo after a read marker: activity %d, want an event's", ch.Act)
	}
	a.runScript([]script.Action{{Echo: &script.Echo{Network: n.Name, Window: "#go", Color: -1, Text: "hi", Message: true}}})
	if ch.Act != state.ActMsg {
		t.Errorf("echo -m: activity %d, want a message's", ch.Act)
	}
}
