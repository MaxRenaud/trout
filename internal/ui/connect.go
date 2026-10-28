package ui

import (
	"cmp"
	"crypto/tls"
	"errors"
	"net"
	"os"
	"slices"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MaxRenaud/trout/internal/config"
	"github.com/MaxRenaud/trout/internal/irc"
	"github.com/MaxRenaud/trout/internal/state"
)

// setBouncer points trout at bouncer b, starting over with just its
// status window.
func (a *App) setBouncer(b config.Bouncer, password string) {
	a.bnc, a.password = b, password
	a.home = state.NewNetwork("Bouncer")
	a.home.Nick = b.User
	if hp, _, err := irc.ParseAddress(b.Address); err == nil {
		host, port, _ := net.SplitHostPort(hp)
		a.home.Server = host
		a.home.Port, _ = strconv.Atoi(port)
	}
	a.nets = []*state.Network{a.home}
	a.active = a.home.Status()
	a.lastQuery, a.nickFocus, a.sel, a.hold = nil, false, nil, nil
	clear(a.batches)
	clear(a.joining)
	clear(a.listing)
	clear(a.listRows)
	clear(a.chanLists)
	clear(a.listWaiting)
	clear(a.history)
	clear(a.unread)
	clear(a.probing)
	clear(a.searching)
	clear(a.upAt)
	a.goingTo = nil
	a.serving, a.focusNet = nil, ""
}

// switchBouncer drops every connection and connects through b instead.
// Lines still arriving from the old connections are for networks that
// are gone, so updateIRC ignores them.
func (a *App) switchBouncer(b config.Bouncer, password string) tea.Cmd {
	if a.control != nil {
		a.control.Close()
		a.control = nil
	}
	for _, c := range a.conns {
		c.Close()
	}
	clear(a.conns)
	a.setBouncer(b, password)
	return a.connectBouncer()
}

func (a *App) notConnected() {
	a.addLine(a.home.Status(), state.RoleInfo, "* Not connected. Use File > Connect to connect to a bouncer.")
}

// stopOn handles the control connection failing in a way retrying won't
// fix: an untrusted or changed certificate, or a wrong password. It asks
// the user instead and reports whether it did.
func (a *App) stopOn(err error) bool {
	status := a.home.Status()
	var ce *irc.CertError
	switch {
	case errors.As(err, &ce) && ce.Pinned == "":
		a.addLine(status, state.RoleQuit, "* "+ce.Addr+" sent a certificate this computer doesn't trust")
		a.openDialog(a.trustBox(ce))
	case errors.As(err, &ce):
		a.addLine(status, state.RoleQuit, "* WARNING: "+ce.Error()+"; not connecting")
		a.openDialog(a.keyChangedBox(ce))
	case errors.Is(err, irc.ErrLogin) && a.bnc.Login == "certificate" && !a.certFailed:
		// The certificate was refused (deleted from another client, say):
		// retry with the password, which is kept for this.
		a.certFailed = true
		a.addLine(status, state.RoleQuit, "* The bouncer refused this computer's certificate; trying the password")
		return false
	case errors.Is(err, irc.ErrLogin):
		a.addLine(status, state.RoleQuit, "* "+err.Error())
		a.openConnect("Login failed. Check the user name and password.")
	default:
		return false
	}
	a.stopped = true
	return true
}

// trustBox asks whether to pin a certificate no authority signed, the
// way SSH asks about a host key it hasn't seen.
func (a *App) trustBox(ce *irc.CertError) *msgBox {
	d := message(
		"The bouncer at "+ce.Addr+" sent a certificate that no",
		"authority this computer knows has signed. That's normal",
		"for a bouncer with a self-signed certificate.",
		"",
		"Its key fingerprint is",
		"    "+ce.Fingerprint,
		"",
		"Trust this key? trout will remember it and refuse to",
		"connect if the bouncer's key ever changes.",
	)
	d.Title = "Unknown Certificate"
	// Cancel has the focus, so a stray Enter doesn't trust a key.
	d.Buttons, d.Sel, d.Cancel = []string{"Trust", "Cancel"}, 1, 1
	d.OnClose = func(a *App, button int) tea.Cmd { return a.trustOrNot(button == 0, ce.Fingerprint) }
	return d
}

// trustOrNot pins fingerprint and connects, or leaves trout disconnected.
func (a *App) trustOrNot(trust bool, fingerprint string) tea.Cmd {
	if !trust {
		a.notConnected()
		return nil
	}
	a.bnc.Pin = fingerprint
	if b := a.savedBouncer(); b != nil {
		b.Pin = fingerprint
		a.saveConfig()
	} else {
		a.addLine(a.home.Status(), state.RoleInfo, "* Trusting that key until you quit (this bouncer isn't in config.toml)")
	}
	return a.connectBouncer()
}

func (a *App) keyChangedBox(ce *irc.CertError) *msgBox {
	d := message(
		"WARNING: the bouncer's key has changed!",
		"",
		"Trusted:   "+ce.Pinned,
		"Presented: "+ce.Fingerprint,
		"",
		"Someone could be intercepting the connection, or the",
		"bouncer has a new key. Only trust the new one if you",
		"know the bouncer's key was changed.",
	)
	d.Title = "Certificate Changed"
	// Cancel has the focus: Enter never accepts a changed key.
	d.Buttons, d.Sel, d.Cancel = []string{"Trust New Key", "Cancel"}, 1, 1
	d.OnClose = func(a *App, button int) tea.Cmd { return a.trustOrNot(button == 0, ce.Fingerprint) }
	return d
}

// savedBouncer is the bouncer in use as config.toml has it, or nil when
// it came from flags only.
func (a *App) savedBouncer() *config.Bouncer {
	if a.cfg == nil || a.bnc.Name == "" {
		return nil
	}
	return a.cfg.Find(a.bnc.Name)
}

func (a *App) saveConfig() {
	if a.cfg == nil {
		return
	}
	if err := a.cfg.Save(); err != nil {
		a.addLine(a.home.Status(), state.RoleQuit, "* Couldn't save "+a.cfg.Path+": "+err.Error())
	}
}

// openConnect shows the Connect dialog for the bouncer in use, with note
// (a login failure, say) under the fields.
func (a *App) openConnect(note string) { a.openConnectFor(a.bnc, a.password, note, nil) }

// openConnectFor is the Connect dialog for bouncer b: the one in use, a
// saved one being edited, or a new one (no name). back is the dialog to
// return to on Cancel.
func (a *App) openConnectFor(b config.Bouncer, password, note string, back modal) {
	intro := []string{"Connect to your bouncer. trout works with soju bouncers."}
	switch {
	case a.bnc.Address == "" && back == nil:
		intro = []string{"Welcome to trout! It connects to IRC through a", "soju bouncer. Where's yours?"}
	case b.Name != "" && b.PasswordCommand != "":
		intro = []string{"An empty password runs this bouncer's password-command."}
	}
	intro = append(intro, "",
		"Address is ircs://host[:port] for TLS (port 6697),",
		"or irc+insecure://host:port for plain text.")
	title := "Connect to Bouncer"
	if b.Name != "" && back != nil {
		title = "Edit Bouncer - " + b.Name
	}
	f := &formBox{Title: title, Lines: intro, Note: note,
		Fields: []*formField{
			newField("Address:", b.Address, false),
			newField("User:", b.User, false),
			newField("Password:", password, true),
		},
		Buttons: []string{"Connect", "Cancel"},
		OnOK: func(a *App, vals []string) (tea.Cmd, error) {
			return a.connectOK(b, password, vals)
		},
		OnButton: map[string]func(*App) tea.Cmd{"Set One Up...": func(a *App) tea.Cmd { a.openInstall(); return nil }},
		OnCancel: func(a *App) tea.Cmd {
			switch {
			case back != nil:
				a.openDialog(back)
			case a.stopped:
				a.notConnected()
			}
			return nil
		},
	}
	if a.bnc.Address == "" && back == nil { // first run: no bouncer yet? trout can make one
		f.Lines = append(f.Lines, "", "No bouncer yet? Set One Up installs soju on a server of yours.")
		f.Buttons = []string{"Connect", "Set One Up...", "Cancel"}
	}
	// Start in the first empty field, or the password after a failed login.
	f.focus = slices.IndexFunc(f.Fields, func(fl *formField) bool { return len(fl.Edit.Buf) == 0 })
	if f.focus < 0 {
		f.focus = 0
		if note != "" {
			f.focus = 2
		}
	}
	a.openDialog(f)
}

// connectOK saves what the Connect dialog says about bouncer was (its
// password was wasPassword) as the active bouncer, the password in the
// credentials file, and connects to it. A new bouncer is named after its
// host, so typing a known host updates that bouncer.
func (a *App) connectOK(was config.Bouncer, wasPassword string, vals []string) (tea.Cmd, error) {
	addr, user, password := strings.TrimSpace(vals[0]), strings.TrimSpace(vals[1]), vals[2]
	if addr == "" {
		return nil, errors.New("type your bouncer's address")
	}
	hostport, _, err := irc.ParseAddress(addr)
	if err != nil {
		return nil, err
	}
	if user == "" {
		return nil, errors.New("type your bouncer user name")
	}
	host, _, _ := net.SplitHostPort(hostport)
	b := config.Bouncer{Name: cmp.Or(was.Name, host), Address: addr, User: user}

	// Keep what's known about the bouncer: its password command, and its
	// pin if it's still the same host and port.
	prev := was
	if a.cfg != nil && was.Name == "" && a.cfg.Find(host) != nil {
		prev = *a.cfg.Find(host)
	}
	b.PasswordCommand = prev.PasswordCommand
	if hp, _, err := irc.ParseAddress(prev.Address); err == nil && hp == hostport {
		b.Pin = prev.Pin
		if prev.User == user {
			b.Login = prev.Login
		}
	}

	var saveErr error
	if a.cfg != nil {
		a.cfg.Use(b)
		// An unchanged password may have come from password-command, which
		// is where it should stay; so should an empty one.
		changed := password != wasPassword || b.Name != was.Name
		if changed && (password != "" || b.PasswordCommand == "") {
			saveErr = a.cfg.SetPassword(b.Name, password)
		}
		saveErr = errors.Join(saveErr, a.cfg.Save())
	}
	cmd := a.connectWith(b, password)
	if saveErr != nil {
		a.addLine(a.home.Status(), state.RoleQuit, "* Couldn't save the bouncer: "+saveErr.Error())
	}
	return cmd, nil
}

// passwordMsg is a password-command's result, for connecting to b.
type passwordMsg struct {
	b        config.Bouncer
	password string
	err      error
}

// connectWith switches to bouncer b. Without a password it runs b's
// password-command first, with the UI suspended so the command can prompt.
func (a *App) connectWith(b config.Bouncer, password string) tea.Cmd {
	if password != "" || b.PasswordCommand == "" {
		return a.switchBouncer(b, password)
	}
	sh, out := config.PasswordCommand(b.PasswordCommand)
	return tea.ExecProcess(sh, func(err error) tea.Msg {
		return passwordMsg{b, config.FirstLine(out.String()), err}
	})
}

func (a *App) gotPassword(msg passwordMsg) tea.Cmd {
	if msg.err != nil {
		a.addLine(a.home.Status(), state.RoleQuit, "* password-command for "+msg.b.Name+" failed: "+msg.err.Error())
		a.openConnectFor(msg.b, "", "Its password-command failed. Type the password?", nil)
		return nil
	}
	return a.switchBouncer(msg.b, msg.password)
}

// kickWhy is the Kick (why) prompt, which asks for a reason, like mIRC's
// popup with $$?="Reason:". As there, an empty reason cancels.
func (a *App) kickWhy(win *state.Window, nick string, ban bool) *formBox {
	title, verb := "Kick (why)", "Kick"
	if ban {
		title, verb = "Ban, Kick (why)", "Ban and kick"
	}
	return &formBox{Title: title, Lines: []string{verb + " " + nick + " from " + win.Name + "."},
		Fields:  []*formField{newField("Reason:", "", false)},
		Buttons: []string{"OK", "Cancel"},
		OnOK: func(a *App, vals []string) (tea.Cmd, error) {
			reason := strings.TrimSpace(vals[0])
			if reason == "" {
				return nil, nil
			}
			if ban {
				a.sendTo(win, "MODE", win.Name, "+b", nick+"!*@*")
			}
			a.sendTo(win, "KICK", win.Name, nick, reason)
			return nil, nil
		}}
}

// settings and applySettings move the View menu and Options choices to
// and from config.toml.
func (a *App) settings() config.Settings {
	return config.Settings{Theme: themeNames[a.themeIdx],
		Toolbar: a.showToolbar, Switchbar: a.showSwitchbar, Treebar: a.showTreebar, Nicklist: a.showNicklist,
		Timestamps: a.stamps, ModePrefix: a.modePrefix, LineMarker: a.showMarker, LineShading: a.shading,
		NickColors: a.nickColors, TreebarWidth: a.treeW, NicklistWidth: a.nickW, Beep: a.beep,
		Colors:      a.colorsSetting(),
		ConfirmExit: a.confirmExit, CmdPrefix: a.cmdPrefix, TabSuffix: a.tabSuffix, Buffer: state.MaxLines,
		SortSwitch: a.sortSwitch, FillSwitch: a.fillSwitch, HiActive: a.hiActive,
		PrivateTo: privateModes[a.privateTo], ShowAddress: a.showAddress, NicksOnJoin: a.nicksOnJoin,
		NotifyActive:    a.notifyActive,
		TimestampFormat: a.stampFormat, RejoinOnKick: a.rejoinOnKick, JoinOnInvite: a.joinOnInvite,
		NoticesActive: a.noticesActive, WhoisOnQuery: a.whoisOnQuery, ConfirmPaste: a.confirmPaste,
		StripCodes: a.stripCodes, KeepOpen: a.keepOpenOnPart, PrefixOwn: a.prefixOwn, CTCPActive: a.ctcpActive,
		AwayCancel: a.awayCancel, Invisible: a.invisible, PopFavorites: a.popFavorites, DCCChat: dccModes[a.dccChat], DCCClose: a.dccClose,
		DCCGet: dccModes[a.dccGet], DCCAutoAsked: a.dccAutoAsked, DCCWarn: a.dccWarn, DownloadDir: a.downloadDir,
		DCCIgnoreMode: dccIgnoreModes[a.dccIgnoreMode], DCCTimeout: a.dccTimeoutS, OptionKey: a.optionKey}
}

func (a *App) applySettings(s config.Settings) {
	a.themeIdx = max(0, slices.Index(themeNames, s.Theme))
	a.showToolbar, a.showSwitchbar, a.showTreebar, a.showNicklist = s.Toolbar, s.Switchbar, s.Treebar, s.Nicklist
	a.stamps, a.modePrefix, a.showMarker, a.shading = s.Timestamps, s.ModePrefix, s.LineMarker, s.LineShading
	a.nickColors, a.treeW, a.nickW, a.beep = s.NickColors, s.TreebarWidth, s.NicklistWidth, s.Beep
	a.colors, a.dccClose = parseColors(s.Colors), s.DCCClose
	a.confirmExit, a.cmdPrefix, a.tabSuffix = s.ConfirmExit, cmp.Or(s.CmdPrefix, "/"), s.TabSuffix
	state.MaxLines = max(100, cmp.Or(s.Buffer, 500))
	a.sortSwitch, a.fillSwitch, a.hiActive = s.SortSwitch, s.FillSwitch, s.HiActive
	a.privateTo = max(0, slices.Index(privateModes, s.PrivateTo))
	a.showAddress, a.nicksOnJoin, a.notifyActive = s.ShowAddress, s.NicksOnJoin, s.NotifyActive
	a.stampFormat, a.rejoinOnKick, a.joinOnInvite = cmp.Or(s.TimestampFormat, "[HH:nn]"), s.RejoinOnKick, s.JoinOnInvite
	a.noticesActive, a.whoisOnQuery, a.confirmPaste = s.NoticesActive, s.WhoisOnQuery, s.ConfirmPaste
	a.stripCodes, a.keepOpenOnPart, a.prefixOwn, a.ctcpActive = s.StripCodes, s.KeepOpen, s.PrefixOwn, s.CTCPActive
	a.awayCancel, a.invisible, a.popFavorites = s.AwayCancel, s.Invisible, s.PopFavorites
	a.dccChat = max(0, slices.Index(dccModes, s.DCCChat))
	a.optionKey = s.OptionKey
	a.dccIgnoreMode, a.dccTimeoutS = max(0, slices.Index(dccIgnoreModes, s.DCCIgnoreMode)), cmp.Or(s.DCCTimeout, 300)
	a.dccGet, a.dccAutoAsked, a.dccWarn, a.downloadDir = max(0, slices.Index(dccModes, s.DCCGet)), s.DCCAutoAsked, s.DCCWarn, s.DownloadDir
}

// saveSettings writes the settings to config.toml whenever they change,
// once a drag has finished.
func (a *App) saveSettings() {
	if a.cfg == nil || a.drag != dragNone {
		return
	}
	if s := a.settings(); s != a.saved {
		a.saved, a.cfg.Settings = s, s
		a.saveConfig()
	}
}

// loadClientCert is this computer's client certificate, made the first time
// a TLS bouncer is used. Without a config there's none.
func (a *App) loadClientCert() *tls.Certificate {
	if a.clientCert != nil || a.cfg == nil {
		return a.clientCert
	}
	cert, err := a.cfg.ClientCert()
	if err != nil {
		a.addLine(a.home.Status(), state.RoleInfo, "* No client certificate: "+err.Error())
		return nil
	}
	a.clientCert = &cert
	return a.clientCert
}

// pinClientCert asks the bouncer to accept this computer's certificate
// after a password login (soju.im/client-cert), so the next logins don't
// need the password.
func (a *App) pinClientCert(c *irc.Conn) {
	if c.Mech != "PLAIN" || !c.Caps["soju.im/client-cert"] || a.clientCert == nil {
		return
	}
	host, _ := os.Hostname()
	c.Send("CLIENTCERT", "CREATE", "trout on "+cmp.Or(host, "this computer"))
}

// clientCertPinned is the bouncer's yes: from now on, log in with the
// certificate, keeping the password as a fallback.
func (a *App) clientCertPinned() {
	if a.bnc.Login == "certificate" && !a.certFailed {
		return
	}
	a.bnc.Login, a.certFailed = "certificate", false
	if b := a.savedBouncer(); b != nil {
		b.Login = "certificate"
		a.saveConfig()
	}
	a.addLine(a.home.Status(), state.RoleInfo, "* The bouncer now knows this computer's certificate: trout logs in with it from now on (the password stays as a fallback)")
}
