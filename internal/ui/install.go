package ui

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MaxRenaud/trout/internal/cloud"
	"github.com/MaxRenaud/trout/internal/config"
	"github.com/MaxRenaud/trout/internal/install"
	"github.com/MaxRenaud/trout/internal/state"
)

// installWizard is Tools > Set Up Bouncer for real (the demo keeps
// setupWizard, the mock-up with VPS providers): soju on a Linux server you
// can SSH into, by internal/install. The same steps update or uninstall
// one it set up (Bouncers > Update and Remove): connect with ssh, find out
// what the server is, get root (as root, sudo, sudo with your password, or
// a script you run), upload, run the setup script, log in to check.
type installWizard struct {
	mode    string // "" installs; or install's "update", "remove", "purge"
	step    int
	hosts   []string // ~/.ssh/config's
	choice  int      // a host, or len(hosts) for the one typed in
	typed   string   // [user@]host, from Another Server or the bouncer's ssh
	account string   // the soju user to make
	b       config.Bouncer

	// A new server at a cloud provider (vps.go): where is 0 for your own
	// server, else whereOptions' provider.
	where      int
	provID     string
	token      string
	prov       cloud.Provider
	regions    []cloud.Region
	region     int
	regionTop  int
	server     *cloud.Server // once created
	serverName string

	remote remote
	in     *install.Installer
	host   string // the server's real name or address (ssh -G)
	log    []string
	err    string // why it failed, or (by hand) what's still missing
	asked  bool   // ssh already had the terminal once
	seq    int    // messages from a cancelled try don't count
	ctx    context.Context
	cancel context.CancelFunc
}

const (
	instWhere = iota
	instRegion
	instPick
	instWorking
	instByHand
	instDone
	instFailed
)

// remote is the server over ssh; tests use a fake (installHooks).
type remote interface {
	install.Runner
	Resolve(ctx context.Context) (host, user string, err error)
	Interactive() *exec.Cmd
}

// installHooks stand in for the parts of setting up a bouncer that reach
// out: ssh, the soju binaries, and logging in to check. Tests set them.
type installHooks struct {
	remote   func(host string) remote
	binaries func(arch string) (soju, sojuctl string, err error)
	check    func(ctx context.Context, in *install.Installer) error
	cloud    func(id, token string) cloud.Provider
	sshKey   string
}

type (
	instStepMsg struct {
		w    *installWizard
		seq  int
		line string
	}
	// instPhaseMsg is a step that ran in the background finishing:
	// "probe", "stage", "setup", "check", or "ssh" (ssh had the terminal).
	instPhaseMsg struct {
		w       *installWizard
		seq     int
		phase   string
		facts   install.Facts
		host    string
		regions []cloud.Region
		server  cloud.Server
		err     error
	}
)

func (a *App) remote(host string) remote {
	if a.hooks != nil && a.hooks.remote != nil {
		return a.hooks.remote(host)
	}
	return install.SSH{Host: host}
}

// checkInstall logs in to the bouncer in sets up, to see it works.
func (a *App) checkInstall(in *install.Installer) func(context.Context) error {
	if a.hooks != nil && a.hooks.check != nil {
		return func(ctx context.Context) error { return a.hooks.check(ctx, in) }
	}
	return in.Check
}

func (a *App) openInstall() {
	a.openDialog(&installWizard{step: instWhere, hosts: sshConfigHosts(), account: a.defaultAccount()})
}

// openReinstall updates or uninstalls bouncer b, which trout set up (mode
// "update", "remove" or "purge"), straight away.
func (a *App) openReinstall(b config.Bouncer, mode string) tea.Cmd {
	w := &installWizard{mode: mode, b: b, typed: b.SSH, account: b.User}
	a.openDialog(w)
	return w.start(a)
}

var accountChars = regexp.MustCompile(`[^A-Za-z0-9_.-]`)

// defaultAccount names your soju account like the bouncer in use does, or
// like you are on this computer.
func (a *App) defaultAccount() string {
	name := a.bnc.User
	if name == "" {
		if u, err := user.Current(); err == nil {
			name = filepath.Base(u.Username) // Windows says DOMAIN\name
		}
	}
	return cmp.Or(accountChars.ReplaceAllString(name, ""), "trout")
}

// sshConfigHosts is the concrete Host entries in ~/.ssh/config.
func sshConfigHosts() []string {
	var hosts []string
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(home, ".ssh", "config"))
	if err != nil {
		return nil
	}
	for line := range strings.Lines(string(data)) {
		f := strings.Fields(line)
		if len(f) < 2 || !strings.EqualFold(f[0], "Host") {
			continue
		}
		for _, h := range f[1:] {
			if !strings.ContainsAny(h, "*?!") {
				hosts = append(hosts, h)
			}
		}
	}
	return hosts
}

// target is what ssh is told to connect to.
func (w *installWizard) target() string {
	if w.choice < len(w.hosts) && w.mode == "" && w.server == nil {
		return w.hosts[w.choice]
	}
	return w.typed
}

// targetName is the target without the user, for the bouncer's name.
func (w *installWizard) targetName() string {
	t := w.target()
	if _, host, ok := strings.Cut(t, "@"); ok {
		return host
	}
	return t
}

func (w *installWizard) say(s string) { w.log = append(w.log, s) }

// start connects and goes through the steps from the top.
func (w *installWizard) start(a *App) tea.Cmd {
	if w.cancel != nil {
		w.cancel()
	}
	w.seq++
	w.step, w.log, w.err, w.in = instWorking, nil, "", nil
	w.ctx, w.cancel = context.WithCancel(context.Background())
	a.installing = w
	if w.server != nil {
		w.remote = a.cloudRemote(w.target())
		w.say("Connecting to it with ssh (a new server takes a minute)...")
	} else {
		w.remote = a.remote(w.target())
		w.say("Connecting to " + w.target() + " with ssh...")
	}
	return w.probe()
}

func (w *installWizard) probe() tea.Cmd {
	r, seq, ctx, fresh := w.remote, w.seq, w.ctx, w.server != nil
	return func() tea.Msg {
		host, _, err := r.Resolve(ctx)
		if err != nil {
			return instPhaseMsg{w: w, seq: seq, phase: "probe", err: err}
		}
		f, err := install.Probe(ctx, r)
		// A new server's sshd (and root's key) can take a minute.
		for deadline := time.Now().Add(3 * time.Minute); fresh && err != nil && time.Now().Before(deadline); {
			var se *install.SSHError
			if !errors.As(err, &se) || (se.Kind != install.SSHUnreachable && se.Kind != install.SSHAuth && se.Kind != install.SSHOther) {
				break
			}
			select {
			case <-ctx.Done():
				return instPhaseMsg{w: w, seq: seq, phase: "probe", err: ctx.Err()}
			case <-time.After(3 * time.Second):
			}
			f, err = install.Probe(ctx, r)
		}
		return instPhaseMsg{w: w, seq: seq, phase: "probe", facts: f, host: host, err: err}
	}
}

// run does one step in the background.
func (w *installWizard) run(phase string, step func(context.Context) error) tea.Cmd {
	seq, ctx := w.seq, w.ctx
	return func() tea.Msg { return instPhaseMsg{w: w, seq: seq, phase: phase, err: step(ctx)} }
}

func (a *App) installUpdate(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case instStepMsg:
		if msg.w == a.installing && msg.seq == msg.w.seq {
			msg.w.say(msg.line)
		}
	case instPhaseMsg:
		if msg.w == a.installing && msg.seq == msg.w.seq {
			return msg.w.phaseDone(a, msg)
		}
	}
	return nil
}

// phaseDone goes on to the next step, or asks, or stops.
func (w *installWizard) phaseDone(a *App, m instPhaseMsg) tea.Cmd {
	if cmd, ok := w.cloudPhase(a, m); ok {
		return cmd
	}
	var se *install.SSHError
	if errors.As(m.err, &se) && se.NeedsYou() {
		if !w.asked {
			return w.askSSH(a, se)
		}
		if se.Kind == install.SSHAuth {
			return w.fail("ssh still can't log in by itself: " + se.Stderr + ". trout needs a key " +
				"(ssh-copy-id " + w.target() + ") or one loaded in your ssh agent (ssh-add), not a password.")
		}
	}
	switch m.phase {
	case "ssh":
		if m.err != nil {
			return w.fail("ssh didn't log in: " + m.err.Error())
		}
		w.say("ssh logged in")
		return w.probe()
	case "probe":
		if m.err != nil {
			return w.fail(m.err.Error())
		}
		return w.probed(a, m.facts, m.host)
	case "stage":
		if m.err != nil {
			return w.fail(m.err.Error())
		}
		return w.setup(a)
	case "setup":
		switch {
		case errors.Is(m.err, install.ErrSudoPassword):
			w.say("sudo didn't take that password")
			w.in.SudoPassword = ""
			return w.askPassword(a, "That password didn't work. Try again.")
		case errors.Is(m.err, install.ErrNoSudo):
			w.say("sudo doesn't let " + w.in.Facts.User + " be root")
			w.in.Root = install.ByHand
			return w.setup(a)
		case m.err != nil:
			return w.fail(m.err.Error())
		}
		return w.check(a)
	case "check":
		if m.err != nil {
			if w.step == instByHand {
				w.err = m.err.Error() // still to do; Check again later
				return nil
			}
			return w.fail(m.err.Error())
		}
		return w.done()
	}
	return nil
}

func (w *installWizard) probed(a *App, f install.Facts, host string) tea.Cmd {
	w.say(fmt.Sprintf("Found %s, %s", cmp.Or(f.OS, "Linux"), f.Machine))
	if why := f.Unsupported(); why != "" {
		return w.fail(why)
	}
	w.host = host
	var err error
	if w.mode == "" {
		w.in, err = install.New(w.remote, f, host, w.account)
	} else {
		w.in, err = install.Existing(w.remote, f, host, w.b.User, a.bouncerPassword(w.b), w.b.Pin, w.mode)
	}
	if err != nil {
		return w.fail(err.Error())
	}
	seq := w.seq
	w.in.Progress = func(s string) { a.send(instStepMsg{w, seq, s}) }
	w.in.Dial = a.dialer(false)
	if a.hooks != nil {
		w.in.Binaries = a.hooks.binaries
	}
	switch w.in.Root {
	case install.AsRoot:
		w.say("Logged in as root")
	case install.WithSudo:
		w.say("sudo works without a password")
	case install.WithPassword:
		w.say("sudo wants " + f.User + "'s password")
	case install.ByHand:
		w.say("There's no sudo on this server")
	}
	return w.run("stage", w.in.StageFiles)
}

// setup runs the script as root, after asking for sudo's password if
// that's the way; ByHand waits for you to run it.
func (w *installWizard) setup(a *App) tea.Cmd {
	switch {
	case w.in.Root == install.ByHand:
		w.step, w.err = instByHand, ""
		return nil
	case w.in.Root == install.WithPassword && w.in.SudoPassword == "":
		return w.askPassword(a, "")
	}
	return w.run("setup", w.in.Setup)
}

func (w *installWizard) check(a *App) tea.Cmd {
	if w.mode == "remove" || w.mode == "purge" {
		return w.done()
	}
	w.say("Logging in to check that it works...")
	return w.run("check", a.checkInstall(w.in))
}

func (w *installWizard) done() tea.Cmd {
	w.step = instDone
	switch w.mode {
	case "":
		w.say("Your bouncer is ready. Its TLS key is pinned: " + w.in.Pin)
	case "update":
		w.say("soju " + install.SojuVersion + " is running")
	default:
		w.say("soju is gone from " + w.host)
	}
	in := w.in
	return func() tea.Msg { // the uploaded files; failing to delete them is fine
		in.Cleanup(context.Background())
		return nil
	}
}

func (w *installWizard) fail(why string) tea.Cmd {
	w.step, w.err = instFailed, why
	return nil
}

func (w *installWizard) close(a *App) {
	if w.cancel != nil {
		w.cancel()
	}
	if a.installing == w {
		a.installing = nil
	}
	a.dlg = nil
}

// askSSH explains that ssh needs to ask you something, then hands it the
// terminal for a moment.
func (w *installWizard) askSSH(a *App, se *install.SSHError) tea.Cmd {
	what := "accept " + w.target() + "'s host key (check its fingerprint)"
	if se.Kind == install.SSHAuth {
		what = "log in: unlock your key, or type a password"
	}
	d := message("ssh needs you to "+what+".", "",
		"trout hands ssh the terminal for a moment and comes", "back when it has logged in.")
	d.Title = "ssh"
	d.Buttons, d.Cancel = []string{"Open ssh", "Cancel"}, 1
	seq := w.seq
	d.OnClose = func(a *App, button int) tea.Cmd {
		if button != 0 {
			w.close(a)
			return nil
		}
		w.asked = true
		a.openDialog(w)
		w.say("Handing ssh the terminal...")
		return tea.ExecProcess(w.remote.Interactive(), func(err error) tea.Msg {
			return instPhaseMsg{w: w, seq: seq, phase: "ssh", err: err}
		})
	}
	a.openDialog(d)
	return nil
}

// askPassword asks for sudo's password over the wizard. It's kept only in
// memory, for this setup.
func (w *installWizard) askPassword(a *App, note string) tea.Cmd {
	f := w.in.Facts
	lines := []string{"trout needs root on " + w.target() + " to set soju up,", "and sudo asks for " + f.User + "'s password."}
	if !f.SudoGroup {
		lines = append(lines, "", f.User+" isn't in sudo, wheel or admin, so sudo may say no.",
			"Run It Myself gives you a command to run as root instead.")
	}
	form := &formBox{Title: "sudo Password", Lines: lines, Note: note,
		Fields:  []*formField{newField("Password:", "", true)},
		Buttons: []string{"OK", "Run It Myself", "Cancel"},
		OnOK: func(a *App, vals []string) (tea.Cmd, error) {
			if vals[0] == "" {
				return nil, errors.New("type the password, or choose Run It Myself")
			}
			w.in.SudoPassword = vals[0]
			a.openDialog(w)
			return w.run("setup", w.in.Setup), nil
		},
		OnButton: map[string]func(*App) tea.Cmd{"Run It Myself": func(a *App) tea.Cmd {
			w.in.Root = install.ByHand
			a.openDialog(w)
			return w.setup(a)
		}},
		OnCancel: func(a *App) tea.Cmd { w.close(a); return nil },
	}
	a.openDialog(form)
	return nil
}

// anotherServer asks for a server that isn't in ~/.ssh/config.
func (w *installWizard) anotherServer(a *App) {
	a.openDialog(&formBox{Title: "Another Server",
		Lines:   []string{"The server to set soju up on, the way you'd give it to ssh:", "user@host, or a host."},
		Fields:  []*formField{newField("Server:", w.typed, false)},
		Buttons: []string{"OK", "Cancel"},
		OnOK: func(a *App, vals []string) (tea.Cmd, error) {
			s := strings.TrimSpace(vals[0])
			if s == "" || strings.ContainsAny(s, " \t'\"") || strings.HasPrefix(s, "-") {
				return nil, errors.New("type it like ssh takes it: user@host")
			}
			w.typed, w.choice = s, len(w.hosts)
			a.openDialog(w)
			return nil, nil
		},
		OnCancel: func(a *App) tea.Cmd { a.openDialog(w); return nil },
	})
}

// viewScript shows the setup script, settings and all, as it is on the
// server.
func (w *installWizard) viewScript(a *App) {
	a.openDialog(&listBox{Title: w.in.Stage() + "/setup.sh", Rows: strings.Split(w.in.Script(), "\n"),
		Buttons: []string{"Close"}, OnClose: func(a *App) { a.openDialog(w) }})
}

func (w *installWizard) title() string {
	switch w.mode {
	case "update":
		return "Update Bouncer"
	case "remove", "purge":
		return "Uninstall Bouncer"
	}
	return "Set Up a Bouncer"
}

func (w *installWizard) size(a *App) (int, int) { return 72, 19 }

func (w *installWizard) buttons() []string {
	switch w.step {
	case instWhere:
		return []string{"Next", "Cancel"}
	case instRegion:
		return []string{"Create Server", "Back", "Cancel"}
	case instPick:
		return []string{"Install", "Cancel"}
	case instWorking:
		return []string{"Cancel"}
	case instByHand:
		if w.mode == "remove" || w.mode == "purge" {
			return []string{"Done", "View Script", "Cancel"}
		}
		return []string{"Check", "View Script", "Cancel"}
	case instFailed:
		if w.server != nil {
			return []string{"Try Again", "Delete Server", "Close"}
		}
		return []string{"Try Again", "Close"}
	}
	return []string{"Finish"}
}

func (w *installWizard) options() []string {
	opts := append([]string{}, w.hosts...)
	other := "Another server..."
	if w.typed != "" {
		other = "Another server: " + w.typed
	}
	return append(opts, other)
}

func (w *installWizard) draw(a *App, c *Canvas, x, y, wd, h int) {
	t := a.theme()
	face := Style{FG: t.FaceText, BG: t.Face}
	bold := Style{FG: t.FaceText, BG: t.Face, Bold: true}
	tx, tw, row := x+3, wd-6, y+2
	text := func(line string, st Style) {
		for _, l := range wrapPlain(line, tw) {
			c.Text(tx, row, tw, l, st)
			row++
		}
	}
	switch w.step {
	case instWhere:
		text("Where should your bouncer run?", bold)
		row++
		for i, o := range whereOptions {
			box := "( ) "
			if i == w.where {
				box = "(•) "
			}
			c.Text(tx, row, tw, box+o, face)
			a.addRegion(Region{X: tx - 1, Y: row, W: tw + 2, H: 1, Kind: HitDlg, S: "where", I: i})
			row++
		}
		row++
		text(whereNotes[w.where], face)
		row++
		text("trout installs soju there and moves your networks onto it.", face)
	case instRegion:
		w.drawRegions(a, c, x, y, wd, h, text)
	case instPick:
		text("trout sets up soju, the bouncer, on a Linux server you can", face)
		text("SSH into, and moves your networks onto it.", face)
		row++
		text("Which server? (from ~/.ssh/config)", bold)
		for i, o := range w.options()[max(0, len(w.hosts)-8):] {
			i += max(0, len(w.hosts)-8)
			box := "( ) "
			if i == w.choice {
				box = "(•) "
			}
			c.Text(tx, row, tw, box+o, face)
			a.addRegion(Region{X: tx - 1, Y: row, W: tw + 2, H: 1, Kind: HitDlg, S: "opt", I: i})
			row++
		}
		row++
		text("Your account on it will be "+w.account+". Nothing changes on the", face)
		text("server until you press Install.", face)
	case instByHand:
		text("trout can't become root on "+w.target()+" by itself.", bold)
		text("Run this there as root (it's uploaded), then press "+w.buttons()[0]+":", face)
		row++
		text("  "+w.in.ManualCommand(), bold)
		row++
		if w.err != "" {
			text("Not yet: "+w.err, face)
		} else {
			text("It installs soju as a system service. View Script shows it.", face)
		}
	default:
		head := map[int]string{instWorking: "Working on " + w.target() + "...", instDone: "Done.", instFailed: "That didn't work."}[w.step]
		text(head, bold)
		row++
		var lines []string
		for _, l := range w.log {
			for _, r := range wrapPlain(l, tw-2) {
				lines = append(lines, "  "+r)
			}
		}
		if w.step == instFailed {
			lines = append(lines, "")
			for _, l := range wrapPlain(w.err, tw-2) {
				lines = append(lines, "  "+l)
			}
		}
		room := y + h - 5 - row
		for _, l := range lines[max(0, len(lines)-room):] {
			c.Text(tx, row, tw, l, face)
			row++
		}
	}
	a.dlgButtons(c, x, y+h-3, wd, w.buttons(), 0)
}

// wrapPlain breaks s into lines of at most w columns, at spaces.
func wrapPlain(s string, w int) []string {
	var rs []SRune
	for _, r := range s {
		rs = append(rs, SRune{R: r})
	}
	var out []string
	for _, row := range wrap(rs, w, 0) {
		var b strings.Builder
		for _, r := range row {
			b.WriteRune(r.R)
		}
		out = append(out, strings.TrimRight(b.String(), " "))
	}
	if len(out) == 0 {
		return []string{""}
	}
	return out
}

func (w *installWizard) press(a *App, button string) tea.Cmd {
	switch button {
	case "Next":
		if w.where == 0 {
			w.step = instPick
			return nil
		}
		return w.chooseCloud(a)
	case "Back":
		w.step = instWhere
		return nil
	case "Create Server":
		return w.create(a)
	case "Delete Server":
		return w.deleteServer()
	case "Try Again":
		if w.server != nil { // the server's there: set it up again
			return w.start(a)
		}
		if w.provID != "" && w.regions == nil {
			return w.chooseCloud(a)
		}
		return w.start(a)
	case "Install":
		if w.step == instPick && w.choice == len(w.hosts) && w.typed == "" {
			w.anotherServer(a)
			return nil
		}
		return w.start(a)
	case "Check":
		w.say("Checking what the script did...")
		return w.run("check", a.checkInstall(w.in))
	case "Done":
		return w.done()
	case "View Script":
		w.viewScript(a)
	case "Cancel", "Close":
		if w.server != nil && w.step != instDone {
			w.keepOrDelete(a)
			return nil
		}
		w.close(a)
	case "Finish":
		w.close(a)
		return a.finishInstall(w)
	}
	return nil
}

func (w *installWizard) key(a *App, k string) tea.Cmd {
	switch {
	case w.step == instWhere && (k == "up" || k == "down"):
		w.where = (w.where + map[string]int{"up": len(whereOptions) - 1, "down": 1}[k]) % len(whereOptions)
		return nil
	case w.step == instRegion && (k == "up" || k == "down"):
		w.region = clamp(w.region+map[string]int{"up": -1, "down": 1}[k], 0, len(w.regions)-1)
		return nil
	}
	if w.step == instPick {
		n := len(w.options())
		switch k {
		case "up":
			w.choice = (w.choice + n - 1) % n
			return nil
		case "down":
			w.choice = (w.choice + 1) % n
			return nil
		}
	}
	switch k {
	case "enter":
		return w.press(a, w.buttons()[0])
	case "esc":
		return w.press(a, w.buttons()[len(w.buttons())-1])
	}
	return nil
}

func (w *installWizard) mouse(a *App, m tea.MouseMsg, r Region, double bool) tea.Cmd {
	if m.Action != tea.MouseActionPress || m.Button != tea.MouseButtonLeft {
		return nil
	}
	switch r.S {
	case "where":
		w.where = r.I
		if double {
			return w.press(a, "Next")
		}
	case "region":
		w.region = r.I
	case "opt":
		w.choice = r.I
		if r.I == len(w.hosts) && (double || w.typed == "") {
			w.anotherServer(a)
		} else if double {
			return w.press(a, "Install")
		}
	case "button":
		return w.press(a, w.buttons()[r.I])
	case "close":
		if w.step != instWorking || w.in == nil {
			w.close(a)
		}
	}
	return nil
}

// bouncerPassword is b's password from the credentials file, or the one
// in use.
func (a *App) bouncerPassword(b config.Bouncer) string {
	if b.Name == a.bnc.Name || a.cfg == nil {
		return a.password
	}
	pw, _ := a.cfg.StoredPassword(b.Name)
	return pw
}

// finishInstall acts on a finished wizard: a new bouncer is saved and
// used, with your networks moving onto it; an uninstalled one is
// forgotten.
func (a *App) finishInstall(w *installWizard) tea.Cmd {
	switch w.mode {
	case "remove", "purge":
		if a.cfg != nil {
			if err := a.cfg.Remove(w.b.Name); err != nil {
				a.addLine(a.home.Status(), state.RoleQuit, "* Couldn't forget "+w.b.Name+": "+err.Error())
			}
		}
		return nil
	case "update":
		a.addLine(a.home.Status(), state.RoleInfo, "* Bouncer: "+w.b.Name+" now runs soju "+install.SojuVersion)
		return nil
	}
	in := w.in
	name := w.targetName()
	for i := 2; a.cfg != nil && a.cfg.Find(name) != nil; i++ {
		name = fmt.Sprintf("%s-%d", w.targetName(), i)
	}
	b := w.cloudBouncer(config.Bouncer{Name: name, Address: in.Address(), User: in.Account, Pin: in.Pin, SSH: w.target()})
	moving := a.networksToMove()
	if a.cfg != nil {
		a.cfg.Use(b)
		if err := a.cfg.SetPassword(b.Name, in.Password); err != nil {
			a.addLine(a.home.Status(), state.RoleQuit, "* Couldn't save the bouncer's password: "+err.Error())
		}
		a.saveConfig()
	}
	cmd := a.switchBouncer(b, in.Password)
	a.moveTo(b.Name, moving)
	return cmd
}

// moveTo has the networks made on bouncer name once trout is connected to
// it and knows what it has.
func (a *App) moveTo(name string, nets []movedNet) {
	a.moving, a.movingTo = nets, name
	if len(nets) > 0 {
		a.addLine(a.home.Status(), state.RoleInfo, fmt.Sprintf("* Bouncer: moving %d networks here once it's connected", len(nets)))
	}
}

// askMove switches to bouncer b with connect, after asking whether your
// networks come along, when there are any and it's another bouncer.
func (a *App) askMove(b config.Bouncer, connect func(a *App) tea.Cmd, back modal) tea.Cmd {
	nets := a.networksToMove()
	if len(nets) == 0 || b.Name == a.bnc.Name {
		return connect(a)
	}
	var names []string
	for _, n := range nets {
		names = append(names, n.name)
	}
	what := fmt.Sprintf("%d networks", len(nets))
	if len(nets) == 1 {
		what = "1 network"
	}
	d := message("Bring your "+what+" ("+strings.Join(names, ", ")+")", "to "+b.Name+"?", "",
		"Move Them has "+b.Name+" connect to them too, with your channels.",
		"They stay on "+a.bnc.Name+" as well.")
	d.Title = "Switch Bouncer"
	d.Buttons, d.Cancel = []string{"Move Them", "Don't", "Cancel"}, 2
	d.OnClose = func(a *App, button int) tea.Cmd {
		switch button {
		case 0:
			cmd := connect(a)
			a.moveTo(b.Name, nets)
			return cmd
		case 1:
			return connect(a)
		}
		if back != nil {
			a.openDialog(back)
		}
		return nil
	}
	a.openDialog(d)
	return nil
}

// movedNet is a network on the old bouncer, to make on the new one.
type movedNet struct {
	name, addr, nick, user, real string
	channels                     []string
}

func (a *App) networksToMove() []movedNet {
	var out []movedNet
	for _, n := range a.nets[1:] {
		m := movedNet{name: n.Name, addr: netAddress(n), nick: n.Attrs["nickname"],
			user: n.Attrs["username"], real: n.Attrs["realname"]}
		for _, w := range n.Wins {
			if w.Kind == state.KChannel && w.Joined {
				m.channels = append(m.channels, w.Name)
			}
		}
		out = append(out, m)
	}
	return out
}

// moveNetworks has the new bouncer make the networks the old one had,
// and join their channels. soju never hands out network passwords, so
// those have to be set again.
func (a *App) moveNetworks() {
	home := a.home.Status()
	moved := 0
	for _, m := range a.moving {
		if slices.ContainsFunc(a.nets[1:], func(n *state.Network) bool { return strings.EqualFold(n.Name, m.name) }) {
			a.addLine(home, state.RoleInfo, "* Bouncer: it already has "+m.name+", so that one stays as it is")
			continue
		}
		moved++
		args := []string{"network", "create", "-addr", m.addr, "-name", m.name}
		for _, f := range [][2]string{{"-nick", m.nick}, {"-username", m.user}, {"-realname", m.real}} {
			if f[1] != "" {
				args = append(args, f[0], f[1])
			}
		}
		a.serv(a.home, home, false, args...)
		for _, ch := range m.channels {
			a.serv(a.home, home, true, "channel", "create", ch+"/"+m.name)
		}
	}
	if moved > 0 {
		a.addLine(home, state.RoleInfo, fmt.Sprintf("* Bouncer: moved %d networks here. Network passwords (SASL, NickServ) "+
			"stay on the old bouncer: set them again in File > Select Server > Edit.", moved))
	}
	a.moving, a.movingTo = nil, ""
}
