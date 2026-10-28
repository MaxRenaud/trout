//go:build integration

package ui

import (
	"cmp"
	"crypto/sha512"
	"crypto/tls"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MaxRenaud/trout/internal/config"
	"github.com/MaxRenaud/trout/internal/irc"
	"github.com/MaxRenaud/trout/internal/state"
)

// TestAgainstSoju runs the real client logic, without a terminal, against
// the dev environment (dev/compose.yaml) or any soju given by TROUT_ADDR:
//
//	go test -tags integration ./internal/ui -run TestAgainstSoju -v
//
// It connects, waits for soju's networks and their channels, then sends a
// message and waits for it to come back.
func TestAgainstSoju(t *testing.T) {
	a, pump := liveApp(t)
	pump("soju's networks", func() bool { return len(a.nets) > 1 && len(a.conns) == len(a.nets)-1 })
	var ch *state.Window
	pump("a joined channel", func() bool {
		for _, n := range a.nets[1:] {
			for _, w := range n.Wins {
				if w.Kind == state.KChannel && w.Joined && len(w.Nicks) > 0 {
					ch = w
					return true
				}
			}
		}
		return false
	})
	t.Logf("%s on %s: %d nicks, topic %q", ch.Name, ch.Net.Name, len(ch.Nicks), ch.Topic)

	text := "hello from the integration test " + time.Now().Format("15:04:05.000")
	a.say(ch, text)
	pump("our message to come back", func() bool {
		for _, l := range ch.Lines {
			if l.Role == state.RoleOwn && strings.HasSuffix(l.Text, text) {
				return true
			}
		}
		return false
	})
}

// liveApp is an App connected to the dev soju, and pump, which plays Bubble
// Tea's role until done says so: it runs commands and feeds their results
// to Update.
func liveApp(t *testing.T) (*App, func(what string, done func() bool)) {
	return liveAppWith(t, Options{
		Bouncer: config.Bouncer{
			Address: cmp.Or(os.Getenv("TROUT_ADDR"), "irc+insecure://127.0.0.1:6667"),
			User:    cmp.Or(os.Getenv("TROUT_USER"), "tester"), // not your dev session's user
			Pin:     os.Getenv("TROUT_PIN"),                    // for the dev soju's self-signed ircs://127.0.0.1:6697
		},
		Password: cmp.Or(os.Getenv("TROUT_PASSWORD"), "tester"),
	})
}

func liveAppWith(t *testing.T, opts Options) (*App, func(what string, done func() bool)) {
	a := New(opts)
	if os.Getenv("TROUT_DEBUG") != "" { // log raw IRC lines
		a.opts.Debug = func(dir, line string) { t.Log(dir, line) }
	}
	msgs := make(chan tea.Msg, 256)
	a.send = func(m tea.Msg) { msgs <- m }
	run := func(cmd tea.Cmd) {
		if cmd != nil {
			go func() { msgs <- cmd() }()
		}
	}
	pump := func(what string, done func() bool) {
		t.Helper()
		deadline := time.After(20 * time.Second)
		for !done() {
			select {
			case m := <-msgs:
				switch m := m.(type) {
				case nil:
				case tea.BatchMsg:
					for _, c := range m {
						run(c)
					}
				default:
					_, cmd := a.Update(m)
					run(cmd)
				}
			case <-deadline:
				t.Fatalf("timed out waiting for %s; soju window says:\n%s", what, dump(a.home.Status()))
			}
		}
	}
	run(a.Init())
	return a, pump
}

// TestSojuManagement manages the dev soju's networks the way the menus
// do: disconnect and reconnect DevNet, detach #dev and join it back, then
// add a network and delete it again.
//
//	go test -tags integration ./internal/ui -run TestSojuManagement -v
func TestSojuManagement(t *testing.T) {
	a, pump := liveApp(t)
	var dev *state.Network
	pump("DevNet", func() bool {
		for _, n := range a.nets[1:] {
			if n.Name == "DevNet" && a.conns[n] != nil && n.Find(state.KChannel, "#dev") != nil {
				dev = n
			}
		}
		return dev != nil && dev.Find(state.KChannel, "#dev").Joined
	})
	noServQuery := func() {
		t.Helper()
		for _, n := range a.nets {
			if n.Find(state.KQuery, "BouncerServ") != nil {
				t.Fatalf("trout's BouncerServ commands opened a query on %s", n.Name)
			}
		}
	}

	a.disconnectNet(dev)
	pump("DevNet to go down, disabled", func() bool { return dev.Disabled && !dev.Find(state.KChannel, "#dev").Joined })
	a.connectNet(dev)
	pump("DevNet back with #dev", func() bool { return !dev.Down() && dev.Find(state.KChannel, "#dev").Joined })
	pump("WHO to say who's away in #dev", func() bool { return len(a.quietWho) == 0 })
	noServQuery()

	a.detach(dev.Find(state.KChannel, "#dev"))
	pump("#dev to close on detach", func() bool { return dev.Find(state.KChannel, "#dev") == nil })
	a.ircCommand(dev.Status(), "join #dev")
	pump("#dev back", func() bool { w := dev.Find(state.KChannel, "#dev"); return w != nil && w.Joined })

	name := "Probe" + time.Now().Format("150405")
	if _, err := a.saveNetwork(nil, []string{name, "irc+insecure://troutnet:6667", "probe", "", "", "", "", ""}); err != nil {
		t.Fatal(err)
	}
	var probe *state.Network
	pump("the new network", func() bool {
		for _, n := range a.nets {
			if n.Name == name {
				probe = n
			}
		}
		return probe != nil && a.active.Net == probe
	})
	a.serv(a.home, a.home.Status(), false, "network", "delete", name)
	pump("the new network to go away", func() bool { return !slices.Contains(a.nets, probe) })
	noServQuery()

	// /away shows on the network itself: WHOIS says so.
	a.activate(dev.Status())
	a.setAway("testing trout")
	pump("the network to say we're away", func() bool {
		w := dev.Find(state.KChannel, "#dev")
		i := w.NickIndex(dev.Nick)
		return i >= 0 && w.Nicks[i].Away
	})
	a.ircCommand(dev.Status(), "whois "+dev.Nick)
	pump("WHOIS to show the away message", func() bool {
		return slices.ContainsFunc(dev.Status().Lines, func(l state.Line) bool { return strings.HasSuffix(l.Text, "is away: testing trout") })
	})
	a.setAway("")
}

func dump(w *state.Window) string {
	var b strings.Builder
	for _, l := range w.Lines {
		b.WriteString(stripCodes(l.Text) + "\n")
	}
	return b.String()
}

// TestReadMarkers reads #dev in one client and checks that a second one
// connecting to the same soju gets the read marker.
//
//	go test -tags integration ./internal/ui -run TestReadMarkers -v
func TestReadMarkers(t *testing.T) {
	devChannel := func(a *App) *state.Window {
		for _, n := range a.nets[1:] {
			if n.Name == "DevNet" {
				return n.Find(state.KChannel, "#dev")
			}
		}
		return nil
	}
	a, pump := liveApp(t)
	pump("a message in #dev", func() bool { w := devChannel(a); return w != nil && !w.Latest.IsZero() })
	w := devChannel(a)
	a.activate(w)
	a.Update(readTickMsg{})
	if !w.ReadAt.Equal(w.Latest) {
		t.Fatalf("reading #dev didn't move the read marker: %v, newest %v", w.ReadAt, w.Latest)
	}
	want := w.ReadAt

	b, pumpB := liveApp(t)
	pumpB("the read marker in the other client", func() bool {
		w := devChannel(b)
		return w != nil && !w.ReadAt.Before(want)
	})
}

// TestFilesAndCTCP asks a dev bot for its VERSION, gets the xdcc bot's
// test file over DCC (then again, resumed from the middle), and uploads a
// file through the bouncer's file host.
//
//	go test -tags integration ./internal/ui -run TestFilesAndCTCP -v
func TestFilesAndCTCP(t *testing.T) {
	a, pump := liveApp(t)
	a.downloadDir, a.dccWarn = t.TempDir(), false
	var tn *state.Network
	pump("TroutNet", func() bool {
		for _, n := range a.nets[1:] {
			if n.Name == "TroutNet" && a.conns[n] != nil && n.FileHost != "" {
				tn = n
			}
		}
		return tn != nil
	})
	status := tn.Status()
	a.activate(status)
	has := func(w *state.Window, sub string) func() bool {
		return func() bool {
			return slices.ContainsFunc(w.Lines, func(l state.Line) bool { return strings.Contains(l.Text, sub) })
		}
	}

	a.sendCTCP(status, "alice", "VERSION")
	pump("alice's VERSION reply", has(status, "[alice VERSION reply]: trout dev bot"))

	// The bot's offer points at 127.0.0.1, so trout asks even though we
	// asked for it; accept with A.
	get := func(what, key string) {
		t.Helper()
		before := len(a.transfers)
		a.ircCommand(status, "msg xdcc xdcc send #1")
		pump(what+" dialog", func() bool { d, ok := a.dlg.(*msgBox); return ok && d.Title == "DCC Get" })
		a.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
		pump(what, func() bool { return len(a.transfers) > before && a.transfers[len(a.transfers)-1].done })
		if tr := a.transfers[len(a.transfers)-1]; tr.err != nil {
			t.Fatalf("%s: %v", what, tr.err)
		}
	}
	check := func() {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(a.downloadDir, "trout-test.bin"))
		if err != nil || len(data) != 3<<20 {
			t.Fatalf("downloaded %d bytes, %v", len(data), err)
		}
		for i, b := range data {
			if b != byte(int64(i)*7%251) {
				t.Fatalf("byte %d is %d", i, b)
			}
		}
	}
	get("the download", "a") // Accept
	check()
	os.Truncate(filepath.Join(a.downloadDir, "trout-test.bin"), 1<<20)
	get("the resumed download", "r") // Resume, of Resume / Overwrite / Rename
	if a.transfers[1].from != 1<<20 {
		t.Fatalf("the second download started at %d, want a resume from 1 MB", a.transfers[1].from)
	}
	check()

	// Send File: upload, and the link lands in the channel.
	var ch *state.Window // whichever channel you're in there
	pump("a joined channel on TroutNet", func() bool {
		i := slices.IndexFunc(tn.Wins, func(w *state.Window) bool { return w.Kind == state.KChannel && w.Joined })
		if i >= 0 {
			ch = tn.Wins[i]
		}
		return ch != nil
	})
	path := filepath.Join(t.TempDir(), "hello.txt")
	os.WriteFile(path, []byte("hello from trout"), 0o600)
	a.later(a.upload(ch, ch.Name, path))
	pump("the upload's link in "+ch.Name, has(ch, "http://127.0.0.1:8080/uploads/"))
	link := ch.Lines[slices.IndexFunc(ch.Lines, func(l state.Line) bool { return strings.Contains(l.Text, "/uploads/") })].Text
	link = link[strings.Index(link, "http://"):]
	resp, err := http.Get(link)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "hello from trout" {
		t.Fatalf("the link serves %q", body)
	}
}

// TestUploadHTTPS uploads over HTTPS to the dev soju's self-signed
// listener: refused without the pin, fine with it, and a clear error for a
// file over soju's 50 MiB limit.
//
//	go test -tags integration ./internal/ui -run TestUploadHTTPS -v
func TestUploadHTTPS(t *testing.T) {
	const host = "https://127.0.0.1:8443/uploads"
	c, err := tls.Dial("tcp", "127.0.0.1:8443", &tls.Config{InsecureSkipVerify: true})
	if err != nil {
		t.Skip("no HTTPS listener on the dev soju:", err)
	}
	pin := irc.Fingerprint(c.ConnectionState().PeerCertificates[0])
	c.Close()

	dir := t.TempDir()
	small := filepath.Join(dir, "small.txt")
	os.WriteFile(small, []byte("pinned upload"), 0o600)
	if _, err := uploadFile(host, "tester", "tester", "", small, nil); err == nil || !strings.Contains(err.Error(), "untrusted certificate") {
		t.Fatalf("without the pin: %v, want an untrusted certificate error", err)
	}
	link, err := uploadFile(host, "tester", "tester", pin, small, nil)
	if err != nil || !strings.HasPrefix(link, "http") {
		t.Fatalf("with the pin: %q, %v", link, err)
	}
	if _, err := uploadFile(host, "tester", "wrong", pin, small, nil); err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("with a wrong password: %v, want 403", err)
	}

	big := filepath.Join(dir, "big.bin")
	f, _ := os.Create(big)
	f.Truncate(51 << 20)
	f.Close()
	if _, err := uploadFile(host, "tester", "tester", pin, big, nil); err == nil || !strings.Contains(err.Error(), "too big") {
		t.Fatalf("a 51 MiB file: %v, want the size limit explained", err)
	}
}

// TestChannelsListLive downloads the Channels List from DevNet, then
// reopens it from the cache and refreshes it with Get List.
func TestChannelsListLive(t *testing.T) {
	a, pump := liveApp(t)
	var dev *state.Network
	pump("DevNet", func() bool {
		for _, n := range a.nets[1:] {
			if n.Name == "DevNet" && a.conns[n] != nil {
				dev = n
			}
		}
		return dev != nil
	})
	a.activate(dev.Status())
	a.later(a.openChannelsList(false))
	pump("the channel list", func() bool { c := a.chanLists[dev]; return c != nil && len(c.rows) > 0 })
	first := a.chanLists[dev].at
	a.dlg = nil
	if a.openChannelsList(false) != nil {
		t.Fatal("reopening asked again instead of using the cached list")
	}
	a.later(a.getChannelList(dev, a.dlg.(*listBox)))
	pump("a fresh list", func() bool { return a.chanLists[dev].at.After(first) })
}

// TestClientCertLive logs in to the dev soju over TLS with the password,
// has it accept this computer's certificate, then logs in with the
// certificate alone (a wrong password), and removes it again.
//
//	go test -tags integration ./internal/ui -run TestClientCertLive -v
func TestClientCertLive(t *testing.T) {
	c, err := tls.Dial("tcp", "127.0.0.1:6697", &tls.Config{InsecureSkipVerify: true})
	if err != nil {
		t.Skip("no TLS listener on the dev soju:", err)
	}
	pin := irc.Fingerprint(c.ConnectionState().PeerCertificates[0])
	c.Close()
	cfg, _ := config.Load(filepath.Join(t.TempDir(), "config.toml"))
	b := config.Bouncer{Name: "dev-tls", Address: "ircs://127.0.0.1:6697", User: "tester", Pin: pin}
	cfg.Use(b)

	a, pump := liveAppWith(t, Options{Config: cfg, Bouncer: b, Password: "tester"})
	pump("the bouncer to accept the certificate", func() bool { return a.bnc.Login == "certificate" })
	if saved, _ := config.Load(cfg.Path); saved.Bouncers[0].Login != "certificate" {
		t.Fatal("the certificate login wasn't saved")
	}
	a.control.Close()

	b.Login = "certificate"
	a2, pump2 := liveAppWith(t, Options{Config: cfg, Bouncer: b, Password: "wrong"})
	pump2("a certificate login", func() bool { return a2.control != nil && a2.control.Mech == "EXTERNAL" })

	fp := sha512.Sum512(a2.clientCert.Certificate[0])
	a2.control.Send("CLIENTCERT", "DELETE", hex.EncodeToString(fp[:]))
}

// TestDCCChatLive chats with the xdcc bot over DCC.
//
//	go test -tags integration ./internal/ui -run TestDCCChatLive -v
func TestDCCChatLive(t *testing.T) {
	a, pump := liveApp(t)
	a.dccWarn = false
	var tn *state.Network
	pump("TroutNet", func() bool {
		for _, n := range a.nets[1:] {
			if n.Name == "TroutNet" && a.conns[n] != nil {
				tn = n
			}
		}
		return tn != nil
	})
	a.ircCommand(tn.Status(), "msg xdcc chat")
	pump("the chat offer", func() bool { d, ok := a.dlg.(*msgBox); return ok && d.Title == "DCC Chat" })
	a.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	var w *state.Window
	pump("the bot's hello", func() bool {
		w = tn.Find(state.KChat, "=xdcc")
		return w != nil && slices.ContainsFunc(w.Lines, func(l state.Line) bool { return strings.Contains(l.Text, "hello from xdcc") })
	})
	a.chatSay(w, "trout says hi", false)
	pump("the echo", func() bool {
		return slices.ContainsFunc(w.Lines, func(l state.Line) bool { return l.Text == "<xdcc> you said: trout says hi" })
	})
	a.closeWindow(w)
}

// TestSearchLive says a unique word in #dev, finds it in the dev soju's
// history, and goes to it.
//
//	go test -tags integration ./internal/ui -run TestSearchLive -v
func TestSearchLive(t *testing.T) {
	a, pump := liveApp(t)
	var w *state.Window
	pump("#dev", func() bool {
		for _, n := range a.nets[1:] {
			if n.Name == "DevNet" && a.canSearch(n) {
				w = n.Find(state.KChannel, "#dev")
			}
		}
		return w != nil && w.Joined
	})
	a.activate(w)
	a.View()
	word := "troutmarker" + strconv.FormatInt(time.Now().UnixNano(), 36)
	a.say(w, "searching for "+word)
	pump("our message", func() bool {
		return slices.ContainsFunc(w.Lines, func(l state.Line) bool { return strings.Contains(l.Text, word) })
	})
	if err := a.searchHistory(w, word); err != nil {
		t.Fatal(err)
	}
	pump("search results", func() bool { l, ok := a.dlg.(*listBox); return ok && strings.HasPrefix(l.Title, "Search Results") })
	if l := a.dlg.(*listBox); len(l.Keys) != 1 {
		t.Fatalf("found %v", l.Rows)
	}
	a.send(tea.KeyMsg{Type: tea.KeyEnter})
	pump("the result highlighted in #dev", func() bool { return a.find != nil && a.find.win == w })
}

// TestRealNameLive sets the real name for every network the way Options >
// Connect does, sees it on the network with a WHOIS on ourselves, and puts
// it back.
//
//	go test -tags integration ./internal/ui -run TestRealNameLive -v
func TestRealNameLive(t *testing.T) {
	a, pump := liveApp(t)
	var dev *state.Network
	pump("DevNet", func() bool {
		for _, n := range a.nets[1:] {
			if n.Name == "DevNet" && a.conns[n] != nil && n.Attrs["realname"] != "" {
				dev = n
			}
		}
		return dev != nil
	})
	a.activate(dev.Status())
	before := dev.Attrs["realname"]
	name := "Real " + strconv.FormatInt(time.Now().Unix(), 36)
	whoisSays := func(real string) func() bool {
		return func() bool {
			return slices.ContainsFunc(dev.Status().Lines, func(l state.Line) bool {
				return strings.HasPrefix(l.Text, dev.Nick+" is ") && strings.HasSuffix(l.Text, " "+real)
			})
		}
	}
	a.serv(a.home, a.home.Status(), false, "user", "update", "-realname", name)
	pump("the bouncer's ok", func() bool {
		return slices.ContainsFunc(a.home.Status().Lines, func(l state.Line) bool { return strings.Contains(l.Text, "updated user") })
	})
	time.Sleep(500 * time.Millisecond) // SETNAME on its way
	a.ircCommand(dev.Status(), "whois "+dev.Nick)
	pump("WHOIS to show the new real name", whoisSays(name))
	a.serv(a.home, a.home.Status(), false, "user", "update", "-realname", before)
}

// TestSelfCTCPLive asks yourself for VERSION and PING, like the nicklist
// popup does on your own nick: each gets one answer, though the bouncer
// sends the request twice (its echo and the delivery).
//
//	go test -tags integration ./internal/ui -run TestSelfCTCPLive -v
func TestSelfCTCPLive(t *testing.T) {
	a, pump := liveApp(t)
	var tn *state.Network
	pump("TroutNet", func() bool {
		for _, n := range a.nets[1:] {
			if n.Name == "TroutNet" && a.conns[n] != nil && n.Nick != "" {
				tn = n
			}
		}
		return tn != nil
	})
	st := tn.Status()
	from := len(st.Lines)
	a.sendCTCP(st, tn.Nick, "VERSION")
	a.sendCTCP(st, tn.Nick, "PING")
	count := func(prefix string) int {
		k := 0
		for _, l := range st.Lines[from:] {
			if strings.HasPrefix(l.Text, prefix) {
				k++
			}
		}
		return k
	}
	pump("both replies", func() bool {
		return count("["+tn.Nick+" VERSION reply]") > 0 && count("["+tn.Nick+" PING reply]") > 0
	})
	deadline := time.Now().Add(time.Second) // time for any doubles
	pump("a moment", func() bool { return time.Now().After(deadline) })
	for _, p := range []string{"VERSION]", "VERSION reply]", "PING]", "PING reply]"} {
		if k := count("[" + tn.Nick + " " + p); k != 1 {
			t.Errorf("%q shown %d times, want once:\n%s", p, k, dump(st))
		}
	}
}
