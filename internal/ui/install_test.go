package ui

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ergochat/irc-go/ircmsg"

	"github.com/MaxRenaud/trout/internal/config"
	"github.com/MaxRenaud/trout/internal/install"
	"github.com/MaxRenaud/trout/internal/state"
)

// fakeServer is a server over "ssh" for the setup wizard: it answers the
// probe and runs the setup script in pretend, with sudo the way it says.
type fakeServer struct {
	mu       sync.Mutex
	sudo     string // the probe's sudo= line: "nopasswd", "password" or ""
	password string // what sudo -S takes
	hostKey  bool   // the first command fails like an unknown host key
	ran      []string
}

func (f *fakeServer) Resolve(context.Context) (string, string, error) {
	return "192.0.2.10", "max", nil
}
func (f *fakeServer) Interactive() *exec.Cmd { return exec.Command("true") }

func (f *fakeServer) Run(ctx context.Context, c install.Cmd) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ran = append(f.ran, c.Line)
	if f.hostKey {
		f.hostKey = false
		return &install.SSHError{Kind: install.SSHHostKey, Stderr: "Host key verification failed."}
	}
	in := ""
	if c.Stdin != nil {
		b, _ := io.ReadAll(c.Stdin)
		in = string(b)
	}
	switch {
	case strings.Contains(c.Line, "uname"):
		out := "uname=Linux x86_64\nuid=1000\nuser=max\nhome=/home/max\nsystemd=yes\nos=Debian GNU/Linux 13 (trixie)\ngroups=max sudo\n"
		if f.sudo != "" {
			out += "sudo=" + f.sudo + "\n"
		}
		io.WriteString(c.Stdout, out)
	case strings.Contains(c.Line, "setup.sh") && !strings.HasPrefix(c.Line, "cat "):
		if strings.HasPrefix(c.Line, "sudo -k -S") && in != f.password+"\n" {
			return &install.RemoteError{Line: c.Line, Code: 1, Stderr: "sudo: 1 incorrect password attempt"}
		}
		io.WriteString(c.Stdout, "trout-step: Starting soju\ntrout-step: done\n")
	}
	return nil
}

func (f *fakeServer) lines() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.ran)
}

// installApp is a real-mode App with a temp config and a bouncer whose
// network Libera has #go joined, set up to install on srv.
func installApp(t *testing.T, srv *fakeServer) (*App, chan string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	os.MkdirAll(filepath.Join(home, ".ssh"), 0o700)
	os.WriteFile(filepath.Join(home, ".ssh", "config"), []byte("Host vps\n  HostName 192.0.2.10\nHost *\n  ServerAliveInterval 30\n"), 0o600)
	a, n, sent, feed := optionsApp(t)
	feed(":max!m@h JOIN #go")
	if w := n.Find(state.KChannel, "#go"); w == nil || !w.Joined {
		t.Fatal("no joined #go")
	}
	cfg, err := config.Load(filepath.Join(t.TempDir(), "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	a.cfg = cfg
	bin := t.TempDir()
	for _, b := range []string{"soju", "sojuctl"} {
		os.WriteFile(filepath.Join(bin, b), []byte(b), 0o755)
	}
	a.hooks = &installHooks{
		remote: func(string) remote { return srv },
		binaries: func(string) (string, string, error) {
			return filepath.Join(bin, "soju"), filepath.Join(bin, "sojuctl"), nil
		},
		check: func(context.Context, *install.Installer) error { return nil },
	}
	return a, sent
}

// drive runs cmd and what it leads to, the way Bubble Tea would, with
// what the setup sends (its steps) delivered too. It stops at commands it
// can't run here (handing ssh the terminal, connecting to a bouncer).
func drive(a *App, cmd tea.Cmd) {
	var mu sync.Mutex
	var sent []tea.Msg
	a.send = func(m tea.Msg) { mu.Lock(); sent = append(sent, m); mu.Unlock() }
	queue := []tea.Cmd{cmd}
	for len(queue) > 0 {
		c := queue[0]
		queue = queue[1:]
		if c == nil {
			continue
		}
		msg := c()
		mu.Lock()
		steps := sent
		sent = nil
		mu.Unlock()
		for _, s := range steps {
			a.Update(s)
		}
		switch m := msg.(type) {
		case tea.BatchMsg:
			queue = append(queue, m...)
		case instPhaseMsg:
			_, next := a.Update(m)
			queue = append(queue, next)
		}
	}
}

func wizard(t *testing.T, a *App) *installWizard {
	t.Helper()
	w, ok := a.dlg.(*installWizard)
	if !ok {
		t.Fatalf("the dialog is %T, not the setup wizard", a.dlg)
	}
	return w
}

// TestInstallWizard sets soju up on a server where sudo needs no password,
// from Tools > Set Up Bouncer to the new bouncer in config.toml, with the
// old bouncer's networks queued to move.
func TestInstallWizard(t *testing.T) {
	srv := &fakeServer{sudo: "nopasswd"}
	a, _ := installApp(t, srv)
	a.openInstall()
	w := wizard(t, a)
	if !slices.Equal(w.options(), []string{"vps", "Another server..."}) || w.account != "max" {
		t.Fatalf("pick step: %q, account %q", w.options(), w.account)
	}
	_, cmd := a.Update(tea.KeyMsg{Type: tea.KeyEnter}) // Install
	drive(a, cmd)
	if w.step != instDone {
		t.Fatalf("step %d, err %q, log %q", w.step, w.err, w.log)
	}
	for _, want := range []string{"Found Debian GNU/Linux 13 (trixie), Linux x86_64", "sudo works without a password",
		"Starting soju", "Logging in to check that it works..."} {
		if !slices.Contains(w.log, want) {
			t.Errorf("the log has no %q: %q", want, w.log)
		}
	}
	if !slices.Contains(srv.lines(), "sudo -n sh /home/max/.trout-install/setup.sh ") {
		t.Fatalf("ran %q", srv.lines())
	}
	pin := w.in.Pin
	a.Update(tea.KeyMsg{Type: tea.KeyEnter}) // Finish
	if a.dlg != nil || a.installing != nil {
		t.Fatalf("Finish left %T open", a.dlg)
	}
	b := a.cfg.Find("vps")
	if b == nil || b.Address != "ircs://192.0.2.10:6697" || b.User != "max" || b.Pin != pin || b.SSH != "vps" || a.cfg.Active != "vps" {
		t.Fatalf("saved %+v, active %q", b, a.cfg.Active)
	}
	if pw, _ := a.cfg.StoredPassword("vps"); pw != a.password || len(pw) != 24 {
		t.Fatalf("password %q / %q", pw, a.password)
	}
	if a.movingTo != "vps" || len(a.moving) != 1 || a.moving[0].name != "Libera" || a.moving[0].addr != "ircs://irc.libera.chat:6697" ||
		!slices.Equal(a.moving[0].channels, []string{"#go"}) || a.moving[0].nick != "max" {
		t.Fatalf("moving %+v", a.moving)
	}
}

// TestMoveNetworks: once the new bouncer has listed its networks, trout
// has it make the old one's, except those it already has, and their
// channels.
func TestMoveNetworks(t *testing.T) {
	a, sent := fakeSoju(t)
	a.moveTo(a.bnc.Name, []movedNet{
		{name: "Libera", addr: "ircs://irc.libera.chat:6697", nick: "max", real: "Max", channels: []string{"#go", "#trout"}},
		{name: "OFTC", addr: "ircs://irc.oftc.net:6697", channels: []string{"#debian"}},
	})
	for _, l := range []string{
		"BATCH +n soju.im/bouncer-networks",
		"@batch=n BOUNCER NETWORK 3 name=OFTC;host=irc.oftc.net;port=6697;tls=1;state=connected",
	} {
		m, _ := ircmsg.ParseLine(l)
		a.Update(ircLineMsg{a.home, m})
	}
	if len(a.moving) != 2 {
		t.Fatal("moved before the bouncer's list was complete")
	}
	if len(a.nets) != 2 || a.nets[1].Name != "OFTC" {
		t.Fatalf("the list's networks weren't handled as they came: %d", len(a.nets))
	}
	m, _ := ircmsg.ParseLine("BATCH -n")
	a.Update(ircLineMsg{a.home, m})
	expectSent(t, sent, `PRIVMSG BouncerServ :network create -addr ircs://irc.libera.chat:6697 -name Libera -nick max -realname Max`)
	expectSent(t, sent, `PRIVMSG BouncerServ :channel create #go/Libera`)
	expectSent(t, sent, `PRIVMSG BouncerServ :channel create #trout/Libera`)
	texts := strings.Join(lineTexts(a.home.Status()), "\n")
	if a.moving != nil || !strings.Contains(texts, "already has OFTC") || !strings.Contains(texts, "moved 1 networks") {
		t.Fatalf("after moving: %v\n%s", a.moving, texts)
	}
	select {
	case l := <-sent:
		if strings.Contains(l, "oftc") || strings.Contains(l, "#debian") {
			t.Fatalf("made OFTC again: %q", l)
		}
	default:
	}
}

// TestSwitchMoves: Bouncers > Use on another bouncer asks whether your
// networks come along.
func TestSwitchMoves(t *testing.T) {
	a, _ := installApp(t, &fakeServer{})
	a.cfg.Use(config.Bouncer{Name: "other", Address: "irc+insecure://127.0.0.1:1", User: "max"})
	a.cfg.SetPassword("other", "pw")
	a.openSavedBouncers()
	d := a.dlg.(*savedBouncersBox)
	d.sel = slices.IndexFunc(a.savedBouncers(), func(b config.Bouncer) bool { return b.Name == "other" })
	d.press(a, "Use")
	box, ok := a.dlg.(*msgBox)
	if !ok || box.Title != "Switch Bouncer" || !strings.Contains(box.Lines[0], "1 network (Libera)") {
		t.Fatalf("Use opened %T", a.dlg)
	}
	box.close(a, 0) // Move Them
	if a.bnc.Name != "other" || a.movingTo != "other" || len(a.moving) != 1 || a.cfg.Active != "other" {
		t.Fatalf("switched to %q, moving %v to %q", a.bnc.Name, a.moving, a.movingTo)
	}
}

// TestInstallSudoPassword: sudo wants a password; a wrong one asks again.
func TestInstallSudoPassword(t *testing.T) {
	srv := &fakeServer{sudo: "password", password: "hunter2"}
	a, _ := installApp(t, srv)
	a.openInstall()
	_, cmd := a.Update(tea.KeyMsg{Type: tea.KeyEnter})
	drive(a, cmd)
	f, ok := a.dlg.(*formBox)
	if !ok || f.Title != "sudo Password" || !f.Fields[0].Mask {
		t.Fatalf("no sudo password prompt: %T", a.dlg)
	}
	w := a.installing
	answer := func(pw string) {
		f.Fields[0].Edit = state.EditBox{Buf: []rune(pw), Pos: len(pw)}
		_, cmd := a.Update(tea.KeyMsg{Type: tea.KeyEnter})
		drive(a, cmd)
	}
	answer("nope")
	if f, ok = a.dlg.(*formBox); !ok || !strings.Contains(f.Note, "didn't work") {
		t.Fatalf("a wrong password: %T", a.dlg)
	}
	answer("hunter2")
	if wizard(t, a) != w || w.step != instDone {
		t.Fatalf("step %d, err %q", w.step, w.err)
	}
}

// TestInstallByHand: no sudo at all, so the wizard shows the command to
// run as root, and Check carries on once it's done.
func TestInstallByHand(t *testing.T) {
	srv := &fakeServer{}
	a, _ := installApp(t, srv)
	done := false
	a.hooks.check = func(context.Context, *install.Installer) error {
		if !done {
			return errors.New("soju.service isn't running")
		}
		return nil
	}
	a.openInstall()
	_, cmd := a.Update(tea.KeyMsg{Type: tea.KeyEnter})
	drive(a, cmd)
	w := wizard(t, a)
	if w.step != instByHand || w.in.ManualCommand() != "sudo sh /home/max/.trout-install/setup.sh" ||
		!slices.Equal(w.buttons(), []string{"Check", "View Script", "Cancel"}) {
		t.Fatalf("step %d, buttons %q", w.step, w.buttons())
	}
	if slices.ContainsFunc(srv.lines(), func(l string) bool { return strings.Contains(l, "sh /home/max/.trout-install/setup.sh") }) {
		t.Fatal("trout ran the script itself")
	}
	drive(a, w.press(a, "Check"))
	if w.step != instByHand || w.err != "soju.service isn't running" {
		t.Fatalf("Check before running it: step %d, %q", w.step, w.err)
	}
	w.press(a, "View Script")
	if l, ok := a.dlg.(*listBox); !ok || !slices.Contains(l.Rows, "TROUT_ACCOUNT=max") {
		t.Fatalf("View Script: %T", a.dlg)
	}
	a.Update(tea.KeyMsg{Type: tea.KeyEsc})
	done = true
	drive(a, wizard(t, a).press(a, "Check"))
	if w.step != instDone {
		t.Fatalf("Check after running it: step %d, %q", w.step, w.err)
	}
}

// TestInstallSSHNeedsYou: an unknown host key hands ssh the terminal once,
// then the setup goes on.
func TestInstallSSHNeedsYou(t *testing.T) {
	srv := &fakeServer{sudo: "nopasswd", hostKey: true}
	a, _ := installApp(t, srv)
	a.openInstall()
	_, cmd := a.Update(tea.KeyMsg{Type: tea.KeyEnter})
	w := a.installing
	drive(a, cmd)
	d, ok := a.dlg.(*msgBox)
	if !ok || d.Title != "ssh" || !strings.Contains(d.Lines[0], "host key") {
		t.Fatalf("no ssh prompt: %T", a.dlg)
	}
	if _, cmd = a.Update(tea.KeyMsg{Type: tea.KeyEnter}); cmd == nil || !w.asked || a.dlg != w { // Open ssh
		t.Fatal("Open ssh didn't hand over the terminal")
	}
	drive(a, func() tea.Msg { return instPhaseMsg{w: w, seq: w.seq, phase: "ssh"} }) // ssh logged in
	if w.step != instDone {
		t.Fatalf("after ssh: step %d, %q", w.step, w.err)
	}
}

// TestUninstall removes a bouncer trout set up, history and all, from
// Tools > Bouncers.
func TestUninstall(t *testing.T) {
	srv := &fakeServer{sudo: "nopasswd"}
	a, _ := installApp(t, srv)
	a.cfg.Use(config.Bouncer{Name: "vps", Address: "ircs://192.0.2.10:6697", User: "max", Pin: "SHA256:x", SSH: "vps"})
	a.cfg.Active = a.bnc.Name
	a.openSavedBouncers()
	d := a.dlg.(*savedBouncersBox)
	d.sel = slices.IndexFunc(a.savedBouncers(), func(b config.Bouncer) bool { return b.Name == "vps" })
	if !slices.Contains(d.buttons(a), "Update") {
		t.Fatalf("no Update for a bouncer trout set up: %q", d.buttons(a))
	}
	d.press(a, "Remove")
	box, ok := a.dlg.(*msgBox)
	if !ok || box.Buttons[0] != "Uninstall" {
		t.Fatalf("Remove opened %T", a.dlg)
	}
	box.Checked = true // delete its history too
	drive(a, box.close(a, 0))
	w := wizard(t, a)
	if w.mode != "purge" || w.step != instDone || !slices.Contains(srv.lines(), "sudo -n sh /home/max/.trout-install/setup.sh purge") {
		t.Fatalf("mode %q step %d err %q ran %q", w.mode, w.step, w.err, srv.lines())
	}
	a.Update(tea.KeyMsg{Type: tea.KeyEnter}) // Finish
	if a.cfg.Find("vps") != nil {
		t.Fatal("the uninstalled bouncer is still in config.toml")
	}
}

// runUntil runs cmd (and batches inside it) until one gives a T.
func runUntil[T any](t *testing.T, cmd tea.Cmd) T {
	t.Helper()
	queue := []tea.Cmd{cmd}
	for len(queue) > 0 {
		c := queue[0]
		queue = queue[1:]
		if c == nil {
			continue
		}
		switch m := c().(type) {
		case T:
			return m
		case tea.BatchMsg:
			queue = append(queue, m...)
		}
	}
	var zero T
	t.Fatalf("never got a %T", zero)
	return zero
}
