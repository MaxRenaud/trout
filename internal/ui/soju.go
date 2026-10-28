package ui

import (
	"cmp"
	"errors"
	"fmt"
	"net"
	"regexp"
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ergochat/irc-go/ircmsg"
	"github.com/mattn/go-runewidth"

	"github.com/MaxRenaud/trout/internal/irc"
	"github.com/MaxRenaud/trout/internal/state"
)

// soju is plumbing: File > Connect, Select Server, /server and Detach act
// on IRC networks, and trout asks BouncerServ, soju's service bot, to do
// the work. soju then reports the result through soju.im/bouncer-networks
// (see networkAttrs), so trout never guesses at the outcome.

// servCmd is a BouncerServ command trout sent itself, until its reply.
type servCmd struct {
	conn   *state.Network // whose connection it went out on
	text   string
	reply  *state.Window // where soju's reply goes
	quiet  bool          // show the reply only if it's an error
	echoed bool          // the echo-message copy arrived (or won't)
	sent   time.Time

	// onLine, if set, takes a reply of several lines one at a time, until
	// it says it's done.
	onLine func(text string) (done bool)
}

// serv sends BouncerServ a command on n's connection: the home one for
// network commands, a network's own for its channels. Neither the command
// (which may hold a password) nor soju's one-line reply opens the
// BouncerServ query; the reply goes to reply.
func (a *App) serv(n *state.Network, reply *state.Window, quiet bool, args ...string) bool {
	text := servQuote(args)
	if !a.sendNet(n, "PRIVMSG", "BouncerServ", text) {
		return false
	}
	a.serving = append(a.serving, &servCmd{conn: n, text: text, reply: reply, quiet: quiet,
		echoed: !a.echoed(n), sent: time.Now()})
	return true
}

// servQuote joins arguments for BouncerServ, which splits them like a
// shell does.
func servQuote(args []string) string {
	q := make([]string, len(args))
	esc := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	for i, s := range args {
		q[i] = s
		if s == "" || strings.ContainsAny(s, " \t\"'\\") {
			q[i] = `"` + esc.Replace(s) + `"`
		}
	}
	return strings.Join(q, " ")
}

// servReply drops the echo of a command serv sent and routes soju's
// reply to it. It reports whether m was one of those.
func (a *App) servReply(n *state.Network, m ircmsg.Message) bool {
	// A reply that never came shouldn't swallow later BouncerServ messages.
	a.serving = slices.DeleteFunc(a.serving, func(c *servCmd) bool { return time.Since(c.sent) > 30*time.Second })
	if len(a.serving) == 0 || len(m.Params) < 2 {
		return false
	}
	from, target, text := m.Nick(), m.Params[0], m.Params[1]
	if strings.EqualFold(from, n.Nick) && strings.EqualFold(target, "BouncerServ") {
		i := slices.IndexFunc(a.serving, func(c *servCmd) bool { return c.conn == n && !c.echoed && c.text == text })
		if i >= 0 {
			a.serving[i].echoed = true
		}
		return i >= 0
	}
	if !strings.EqualFold(from, "BouncerServ") || m.Command != "PRIVMSG" {
		return false
	}
	i := slices.IndexFunc(a.serving, func(c *servCmd) bool { return c.conn == n })
	if i < 0 {
		return false
	}
	c := a.serving[i]
	if c.onLine != nil && !strings.HasPrefix(text, "error") {
		if c.onLine(text) {
			a.serving = slices.Delete(a.serving, i, i+1)
		}
		return true
	}
	a.serving = slices.Delete(a.serving, i, i+1)
	failed := strings.HasPrefix(text, "error")
	switch {
	case failed:
		w := c.reply
		if !slices.Contains(w.Net.Wins, w) { // a detached channel, say
			w = w.Net.Status()
		}
		a.addLine(w, state.RoleQuit, "* Bouncer: "+text)
		if a.dlg == nil {
			d := message("The bouncer says:", "", text)
			d.Title = "Bouncer"
			a.openDialog(d)
		}
	case !c.quiet:
		a.addLine(c.reply, state.RoleInfo, "* Bouncer: "+text)
	}
	return true
}

// statusLine is one network in BouncerServ's "network status":
// "Libera (ircs://irc.libera.chat:6697) [connected]: 3 channels".
var statusLine = regexp.MustCompile(`^(.+) \(\S+\) \[([^\]]+)\]`)

// refreshDisabled asks BouncerServ which networks are switched off, which
// soju's network attributes don't say: a disabled network and one soju is
// retrying both look disconnected there.
func (a *App) refreshDisabled() {
	if !a.serv(a.home, a.home.Status(), true, "network", "status") {
		return
	}
	seen := 0
	a.serving[len(a.serving)-1].onLine = func(text string) bool {
		m := statusLine.FindStringSubmatch(text)
		if m == nil {
			return true // "no networks", or something unexpected
		}
		for _, n := range a.nets[1:] {
			if n.Name != m[1] {
				continue
			}
			was := n.Disabled
			n.Disabled = m[2] == "disabled"
			if n.Disabled && !was {
				a.addLine(n.Status(), state.RoleInfo, "* Bouncer: "+n.Name+" is switched off; File > Connect turns it back on")
			}
		}
		seen++
		return seen >= len(a.nets)-1
	}
}

// connected reports whether n is up: for the bouncer's own window, the
// connection to the bouncer; for a network, the bouncer being on it (or
// trying), and trout's connection to that.
func (a *App) connected(n *state.Network) bool {
	if n == a.home {
		return a.control != nil
	}
	return a.conns[n] != nil && !n.Down()
}

// connectNet is File > Connect. For a network, soju connects to it; for
// soju's own status window, trout connects to the bouncer.
func (a *App) connectNet(n *state.Network) tea.Cmd {
	status := n.Status()
	if n == a.home {
		switch {
		case a.control != nil:
			a.addLine(status, state.RoleInfo, "* Already connected to the bouncer")
		case a.bnc.Address == "" || a.bnc.User == "":
			a.openConnect("")
		case a.stopped:
			return a.connectBouncer()
		default:
			a.addLine(status, state.RoleInfo, "* Already trying to connect to the bouncer")
		}
		return nil
	}
	if st := n.Attrs["state"]; st != "" && !n.Down() {
		a.addLine(status, state.RoleInfo, "* The bouncer is already "+st+" to "+n.Name)
		return nil
	}
	a.serv(a.home, status, true, "network", "update", n.Name, "-enabled", "true")
	return nil
}

// disconnectNet is File > Disconnect: soju leaves the network but keeps it
// (and its channels) for File > Connect. On soju's own status window it
// disconnects from the bouncer.
func (a *App) disconnectNet(n *state.Network) {
	if n != a.home {
		a.serv(a.home, n.Status(), true, "network", "update", n.Name, "-enabled", "false")
		return
	}
	if a.control == nil && a.stopped {
		a.addLine(n.Status(), state.RoleInfo, "* Not connected to the bouncer")
		return
	}
	a.switchBouncer(a.bnc, a.password) // close everything and start over...
	a.stopped = true                   // ...without connecting
	a.addLine(a.home.Status(), state.RoleQuit, "* Disconnected from the bouncer. It stays on your networks; File > Connect reconnects.")
}

// detach is the channel popup's Detach: soju stays in the channel but
// parts it on our side, which closes the window. /join brings it back.
func (a *App) detach(w *state.Window) {
	if a.serv(w.Net, w.Net.Status(), true, "channel", "update", w.Name, "-detached", "true") {
		a.addLine(w.Net.Status(), state.RoleInfo, "* Detached from "+w.Name+"; the bouncer stays in it. /join "+w.Name+" brings it back.")
	}
}

// netAddress is a network's address the way soju and the Servers dialog
// write it.
func netAddress(n *state.Network) string {
	hp := net.JoinHostPort(n.Server, fmt.Sprint(n.Port))
	if n.Attrs["tls"] == "0" {
		return "irc+insecure://" + hp
	}
	return "ircs://" + hp
}

// serverCommand is /server. With soju in between, /server host switches to
// the network soju has on that host, or has soju add one. Unlike mIRC it
// never repoints the window's own network: that one keeps its channels
// and history. /server alone reconnects.
func (a *App) serverCommand(w *state.Window, rest string) tea.Cmd {
	var words []string
	for f := range strings.FieldsSeq(rest) {
		if !strings.HasPrefix(f, "-") { // mIRC's -m and friends
			words = append(words, f)
		}
	}
	if len(words) == 0 {
		return a.connectNet(w.Net)
	}
	addr := words[0]
	if len(words) > 1 && !strings.Contains(addr, "://") && !strings.Contains(addr, ":") {
		addr = net.JoinHostPort(addr, words[1]) // /server host port, like mIRC
	}
	hp, _, err := irc.ParseAddress(addr)
	if err != nil {
		a.addLine(w, state.RoleInfo, "* /server: "+err.Error())
		return nil
	}
	host, _, _ := net.SplitHostPort(hp)
	for _, n := range a.nets[1:] {
		if strings.EqualFold(n.Server, host) {
			a.activate(n.Status())
			if n.Down() {
				return a.connectNet(n)
			}
			return nil
		}
	}
	if _, err := a.saveNetwork(nil, []string{"", addr, "", "", "", "", "", ""}); err != nil {
		a.addLine(w, state.RoleInfo, "* /server: "+err.Error())
		return nil
	}
	a.addLine(w, state.RoleInfo, "* Asking the bouncer to add "+host+"...")
	return nil
}

// networkForm adds a network to soju (n is nil) or edits one. back is the
// dialog to return to on Cancel.
func (a *App) networkForm(n *state.Network, back modal) *formBox {
	f := &formBox{Compact: true, Buttons: []string{"OK", "Cancel"},
		OnOK: func(a *App, vals []string) (tea.Cmd, error) { return a.saveNetwork(n, vals) },
		OnCancel: func(a *App) tea.Cmd {
			if back != nil {
				a.openDialog(back)
			}
			return nil
		}}
	name, addr, nick, user, real := "", "", "", "", ""
	if n == nil {
		f.Title = "Add Network"
		f.Lines = []string{"The bouncer connects to it and stays on it.",
			"Address is ircs://host[:port], or irc+insecure://host:port.",
			"Empty names are the ones in Options > Connect."}
	} else {
		f.Title = "Edit Network - " + n.Name
		f.Lines = []string{"Empty password fields keep what the bouncer has.",
			"Saving reconnects the bouncer to the network."}
		name, addr, nick = n.Name, netAddress(n), n.Attrs["nickname"]
		user, real = n.Attrs["username"], n.Attrs["realname"]
	}
	f.Fields = []*formField{
		newField("Name:", name, false),
		newField("Address:", addr, false),
		newField("Nick:", nick, false),
		newField("Username:", user, false),
		newField("Real name:", real, false),
		newField("Server pass:", "", true),
		newField("SASL account:", "", false),
		newField("SASL pass:", "", true),
	}
	if n == nil {
		f.focus = 1
	}
	return f
}

// saveNetwork has BouncerServ create or update a network from the network
// form's fields. A new network with a SASL login starts disabled, so soju's
// first connection already logs in.
func (a *App) saveNetwork(n *state.Network, vals []string) (tea.Cmd, error) {
	for i := range vals {
		vals[i] = strings.TrimSpace(vals[i])
	}
	name, addr, nick, user, real := vals[0], vals[1], vals[2], vals[3], vals[4]
	pass, acct, acctPass := vals[5], vals[6], vals[7]
	if addr == "" {
		return nil, errors.New("type the network's address, like ircs://irc.libera.chat")
	}
	hp, useTLS, err := irc.ParseAddress(addr)
	if err != nil {
		return nil, err
	}
	canon := "irc+insecure://" + hp
	if useTLS {
		canon = "ircs://" + hp
	}
	host, _, _ := net.SplitHostPort(hp)
	name = cmp.Or(name, host)
	if (acct == "") != (acctPass == "") {
		return nil, errors.New("give a SASL account and password, or neither")
	}
	for _, o := range a.nets[1:] {
		if o != n && strings.EqualFold(o.Name, name) {
			return nil, fmt.Errorf("the bouncer already has a network called %s", o.Name)
		}
	}
	if a.control == nil {
		return nil, errors.New("not connected to the bouncer")
	}

	if n == nil {
		home := a.home.Status()
		args := []string{"network", "create", "-addr", canon, "-name", name}
		for _, f := range [][2]string{{"-nick", nick}, {"-username", user}, {"-realname", real}, {"-pass", pass}} {
			if f[1] != "" {
				args = append(args, f[0], f[1])
			}
		}
		if acct != "" {
			args = append(args, "-enabled", "false")
		}
		a.serv(a.home, home, false, args...)
		if acct != "" {
			a.serv(a.home, home, false, "sasl", "set-plain", "-network", name, acct, acctPass)
			a.serv(a.home, home, true, "network", "update", name, "-enabled", "true")
		}
		a.focusNet = name
		return nil, nil
	}

	status := n.Status()
	if acct != "" {
		a.serv(a.home, status, false, "sasl", "set-plain", "-network", n.Name, acct, acctPass)
	}
	args := []string{"network", "update", n.Name}
	if canon != netAddress(n) {
		args = append(args, "-addr", canon)
	}
	if name != n.Name {
		args = append(args, "-name", name)
	}
	if nick != "" && nick != n.Attrs["nickname"] {
		args = append(args, "-nick", nick)
	}
	if user != "" && user != n.Attrs["username"] {
		args = append(args, "-username", user)
	}
	if real != "" && real != n.Attrs["realname"] {
		args = append(args, "-realname", real)
	}
	if pass != "" {
		args = append(args, "-pass", pass)
	}
	if len(args) == 3 {
		if acct == "" {
			return nil, nil // nothing changed
		}
		// Any update reconnects, which is what makes a new login count.
		args = append(args, "-enabled", "true")
	}
	a.serv(a.home, status, false, args...)
	return nil, nil
}

// serversBox is File > Select Server: soju's networks in a list, like
// mIRC's server list, with buttons down the right.
type serversBox struct{ sel, top int }

var serverButtons = []string{"Connect", "Disconnect", "Add...", "Edit...", "Delete", "Close"}

func (a *App) openServers() {
	d := &serversBox{}
	d.sel = max(0, slices.Index(d.nets(a), a.active.Net))
	a.openDialog(d)
}

// nets are the networks listed: soju's, not its own status window. The
// demo has no soju window, so it lists everything.
func (d *serversBox) nets(a *App) []*state.Network {
	if a.opts.Demo {
		return a.nets
	}
	return a.nets[1:]
}

func (d *serversBox) title() string { return "Servers" }

func (d *serversBox) size(a *App) (int, int) { return 78, 16 }

const serverButtonW = 14

func (d *serversBox) rows(a *App) int { return 16 - 5 }

func (d *serversBox) draw(a *App, c *Canvas, x, y, w, h int) {
	t := a.theme()
	nets := d.nets(a)
	lx, ly := x+2, y+2
	lw := w - 4 - serverButtonW - 2
	n := h - 5
	// Name, address, then state ("disconnected" is the longest).
	addrW := lw - 1 - 15 - 13
	c.Text(lx+1, ly, lw-1, fmt.Sprintf("%-14s %-*s %s", "Network", addrW, "Address", "State"),
		Style{FG: t.FaceText, BG: t.Face, Bold: true})
	c.Fill(lx, ly+1, lw, n, Style{BG: t.TextBG})
	if len(nets) == 0 {
		c.Text(lx+1, ly+1, lw-1, "No networks yet. Add one, and the bouncer connects to it.", Style{FG: t.TextFG, BG: t.TextBG})
	}
	d.sel = clamp(d.sel, 0, len(nets)-1)
	d.top = clamp(d.top, max(0, d.sel-n+1), d.sel)
	for i := 0; i < n && d.top+i < len(nets); i++ {
		idx := d.top + i
		nw := nets[idx]
		st := Style{FG: t.TextFG, BG: t.TextBG}
		if nw.Down() {
			st.FG = t.Disabled
		}
		if idx == d.sel {
			st = Style{FG: t.SelFG, BG: t.SelBG}
		}
		addr := netAddress(nw)
		if a.opts.Demo {
			addr = fmt.Sprintf("ircs://%s:%d", nw.Server, nw.Port)
		}
		row := fmt.Sprintf("%-14s %-*s %s", runewidth.Truncate(nw.Name, 14, "…"), addrW,
			runewidth.Truncate(addr, addrW, "…"), cmp.Or(nw.State(), "connected"))
		c.Fill(lx, ly+1+i, lw, 1, st)
		c.Text(lx+1, ly+1+i, lw-1, row, st)
		a.addRegion(Region{X: lx, Y: ly + 1 + i, W: lw, H: 1, Kind: HitDlg, S: "item", I: idx})
	}
	bx := x + w - 2 - serverButtonW
	for i, b := range serverButtons {
		st := Style{FG: t.FaceText, BG: t.Face}
		if i == 0 {
			st.Bold = true // Enter
		}
		pad := serverButtonW - 2 - runewidth.StringWidth(b)
		a.button(c, bx, ly+2*i, strings.Repeat(" ", pad/2)+b+strings.Repeat(" ", pad-pad/2), st, 1)
		a.addRegion(Region{X: bx, Y: ly + 2*i, W: serverButtonW, H: 1, Kind: HitDlg, S: "button", I: i})
	}
}

func (d *serversBox) press(a *App, button string) tea.Cmd {
	nets := d.nets(a)
	var n *state.Network
	if len(nets) > 0 {
		n = nets[clamp(d.sel, 0, len(nets)-1)]
	}
	if button == "Close" {
		a.dlg = nil
		return nil
	}
	if a.opts.Demo {
		m := message("The bouncer manages the networks for real;", "the demo has no bouncer to ask.")
		m.OnClose = func(a *App, _ int) tea.Cmd { a.openDialog(d); return nil }
		a.openDialog(m)
		return nil
	}
	switch {
	case button == "Add...":
		a.openDialog(a.networkForm(nil, d))
	case n == nil:
	case button == "Connect":
		a.dlg = nil
		a.activate(n.Status())
		return a.connectNet(n)
	case button == "Disconnect":
		a.dlg = nil
		a.disconnectNet(n)
	case button == "Edit...":
		a.openDialog(a.networkForm(n, d))
	case button == "Delete":
		a.openDialog(&msgBox{Title: "Delete Network", Buttons: []string{"Yes", "No"}, Sel: 1, Cancel: 1,
			Lines: []string{"Delete " + n.Name + " from the bouncer?", "",
				"The bouncer disconnects from it and forgets it,", "along with its channels."},
			OnClose: func(a *App, button int) tea.Cmd {
				if button == 0 {
					a.serv(a.home, a.home.Status(), false, "network", "delete", n.Name)
				}
				a.openDialog(d)
				return nil
			}})
	}
	return nil
}

func (d *serversBox) key(a *App, k string) tea.Cmd {
	switch k {
	case "up":
		d.sel--
	case "down":
		d.sel++
	case "pgup":
		d.sel -= d.rows(a)
	case "pgdown":
		d.sel += d.rows(a)
	case "home":
		d.sel = 0
	case "end":
		d.sel = len(d.nets(a)) - 1
	case "enter":
		return d.press(a, "Connect")
	case "delete", "backspace":
		return d.press(a, "Delete")
	case "insert":
		return d.press(a, "Add...")
	case "esc":
		a.dlg = nil
	}
	return nil
}

func (d *serversBox) mouse(a *App, m tea.MouseMsg, r Region, double bool) tea.Cmd {
	switch {
	case m.Button == tea.MouseButtonWheelUp:
		d.sel--
	case m.Button == tea.MouseButtonWheelDown:
		d.sel++
	case m.Action != tea.MouseActionPress || m.Button != tea.MouseButtonLeft:
	case r.S == "item":
		d.sel = r.I
		if double {
			return d.press(a, "Connect")
		}
	case r.S == "button":
		return d.press(a, serverButtons[r.I])
	case r.S == "close":
		a.dlg = nil
	}
	return nil
}
