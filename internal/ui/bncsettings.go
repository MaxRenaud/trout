package ui

import (
	"cmp"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MaxRenaud/trout/internal/state"
)

// Bouncer > Settings: what soju keeps for you, in the Options dialog's
// shape, asked of BouncerServ each time it opens (network, channel, user
// and server status) and changed through it. soju only reports some of
// it: a network's connect commands and a channel's detach and relay
// options can be set but not read, so those forms start at "unchanged".
// Not mIRC's: it has no bouncer.

const bncSettingsTitle = "Bouncer Settings"

const (
	bncNetworks = iota
	bncChannels
	bncAccount
	bncUsers
	bncServer
)

// bncState is what BouncerServ last said, for the settings dialog.
type bncState struct {
	networks []string     // network status lines
	channels []chanStatus // channel status
	users    []userStatus // nil unless you're an admin
	admin    bool
	server   string // server status (admins only)
	asked    int    // replies still coming
}

type chanStatus struct{ name, network, status string }

type userStatus struct {
	name, attrs, info string
}

func (c chanStatus) key() string { return c.name + "/" + c.network }

var (
	// "#trout/TroutNet [joined, detached]"
	chanStatusLine = regexp.MustCompile(`^([^/]+)/(.+) \[([^\]]+)\]$`)
	// "trout (admin, disabled): 3 networks"
	userStatusLine = regexp.MustCompile(`^(\S+)(?: \(([^)]*)\))?: (.*)$`)
	// "Libera (ircs://irc.libera.chat:6697) [connected as max]: 3 channels"
	netStatusLine = regexp.MustCompile(`^(.+) \(\S+\) \[([^\]]+)\](?:: (.*))?$`)
)

// openBouncerSettings opens Bouncer > Settings at page, asking the
// bouncer for what it shows.
func (a *App) openBouncerSettings(page int) {
	switch {
	case a.opts.Demo:
		m := message("The bouncer keeps these settings for real;", "the demo has no bouncer to ask.")
		m.Title = bncSettingsTitle
		a.openDialog(m)
		return
	case a.control == nil:
		m := message("Not connected to the bouncer.", "", "File > Connect on its status window connects.")
		m.Title = bncSettingsTitle
		a.openDialog(m)
		return
	}
	s := &bncState{asked: 3}
	a.bset = s
	got := func() {
		s.asked--
		a.refreshBouncerSettings()
	}
	a.servList([]string{"network", "status"}, func(lines []string, _ string) {
		s.networks = lines
		got()
	})
	a.servList([]string{"channel", "status"}, func(lines []string, _ string) {
		for _, l := range lines {
			if m := chanStatusLine.FindStringSubmatch(l); m != nil {
				s.channels = append(s.channels, chanStatus{m[1], m[2], m[3]})
			}
		}
		got()
	})
	// Only admins may list users; the error says you aren't one.
	a.servList([]string{"user", "status"}, func(lines []string, err string) {
		if err == "" {
			s.admin, s.users = true, []userStatus{}
			for _, l := range lines {
				if m := userStatusLine.FindStringSubmatch(l); m != nil {
					s.users = append(s.users, userStatus{m[1], m[2], m[3]})
				}
			}
			s.asked++
			a.servList([]string{"server", "status"}, func(lines []string, _ string) {
				s.server = strings.Join(lines, " ")
				got()
			})
		}
		got()
	})
	a.openDialog(&optionsBox{Title: bncSettingsTitle, page: page, pages: a.bncPages()})
}

// refreshBouncerSettings shows what just arrived, if the dialog is open.
func (a *App) refreshBouncerSettings() {
	o, ok := a.dlg.(*optionsBox)
	if !ok || o.Title != bncSettingsTitle {
		return
	}
	o.pages = a.bncPages()
	o.page = min(o.page, len(o.pages)-1)
	o.sel = min(o.sel, max(0, len(o.pages[o.page].Items)-1))
}

// backToSettings is how a dialog opened from the settings returns to them
// (asking again, so a change shows).
func backToSettings(page int) func(*App) { return func(a *App) { a.openBouncerSettings(page) } }

func (a *App) bncPages() []optPage {
	s := a.bset
	if s == nil {
		s = &bncState{}
	}
	loading := func(notes []string) []string {
		if s.asked > 0 {
			return append(notes, "", "Asking the bouncer...")
		}
		return notes
	}

	// Networks: their state, and Select Server for the rest.
	var netNotes []string
	for _, l := range s.networks {
		if m := netStatusLine.FindStringSubmatch(l); m != nil {
			l = m[1] + ": " + m[2]
			if m[3] != "" {
				l += ", " + m[3]
			}
		}
		netNotes = append(netNotes, oneOf(l))
	}
	if len(netNotes) > 0 {
		netNotes = append([]string{""}, netNotes...)
	}
	networks := optPage{Name: "Networks", Notes: loading(netNotes), Items: []optItem{
		{Label: "Networks: connect, edit, delete...", Action: func(a *App) {
			a.openDialog(&serversBox{back: backToSettings(bncNetworks)})
		}},
		{Label: "Add a network...", Action: func(a *App) {
			a.openAddNetwork(&serversBox{back: backToSettings(bncNetworks)})
		}},
	}}

	// Channels: the ones soju keeps, detached or not.
	detached := 0
	for _, c := range s.channels {
		if strings.Contains(c.status, "detached") {
			detached++
		}
	}
	chanNotes := []string{"", fmt.Sprintf("The bouncer keeps %d channels; %d detached.", len(s.channels), detached),
		"Detached ones stay joined on the bouncer,", "without a window here."}
	channels := optPage{Name: "Channels", Notes: loading(chanNotes), Items: []optItem{
		{Label: "Channels: detach, attach, options...", Action: func(a *App) { a.openBouncerChannels(s) }},
	}}

	// Account: your names and password, which every network uses.
	nick, real := "", ""
	if len(a.nets) > 1 {
		id := a.nets[1]
		nick, real = cmp.Or(id.Attrs["nickname"], id.Nick), id.Attrs["realname"]
	}
	acctNotes := []string{"", "User:      " + a.bnc.User}
	if s.admin {
		acctNotes = append(acctNotes, "           an admin (see Users)")
	}
	account := optPage{Name: "Account", Notes: acctNotes, Items: []optItem{
		{Label: "Nickname: " + cmp.Or(nick, "(none)") + "...", Action: func(a *App) {
			a.openIdentity("Nickname", "-nick", nick, backToSettings(bncAccount))
		}},
		{Label: "Real name: " + cmp.Or(real, "(none)") + "...", Action: func(a *App) {
			a.openIdentity("Real name", "-realname", real, backToSettings(bncAccount))
		}},
		{Label: "Password...", Action: (*App).openBouncerPassword},
	}}

	// Server: where it runs, and what trout can do there.
	srvNotes := []string{"", "Address:   " + a.bncAddress()}
	if a.bnc.SojuVersion != "" {
		srvNotes = append(srvNotes, "Runs:      soju "+a.bnc.SojuVersion)
	}
	cert := "self-signed (trout pins its key)"
	if a.bnc.Cert == letsEncrypt {
		cert = "from Let's Encrypt"
	}
	srvNotes = append(srvNotes, "Cert:      "+cert)
	if s.server != "" {
		srvNotes = append(srvNotes, "", "Now:")
		for part := range strings.SplitSeq(s.server, ", ") {
			srvNotes = append(srvNotes, "  "+part)
		}
	}
	server := optPage{Name: "Server", Notes: loading(srvNotes), Items: []optItem{
		{Label: "Connect a phone...", Action: func(a *App) { a.openPhone() }},
	}}
	if a.bnc.SSH != "" { // trout set it up: it can update it
		b := a.bnc
		server.Items = append([]optItem{{Label: "Update, certificate...", Action: func(a *App) {
			a.openServer(b, nil)
		}}}, server.Items...)
	}

	pages := []optPage{networks, channels, account}
	if s.admin {
		var notes []string
		for _, u := range s.users {
			l := u.name + ": " + u.info
			if u.attrs != "" {
				l = u.name + " (" + u.attrs + "): " + u.info
			}
			notes = append(notes, oneOf(l))
		}
		pages = append(pages, optPage{Name: "Users", Notes: append([]string{""}, notes...), Items: []optItem{
			{Label: "Users: add, delete, admin...", Action: func(a *App) { a.openBouncerUsers(s) }},
		}})
	} else {
		pages = append(pages, optPage{Name: "Users", Notes: loading([]string{
			"Only the bouncer's admins can", "manage its users."})})
	}
	return append(pages, server)
}

// openBouncerChannels lists the channels soju keeps, to detach, attach
// and set what happens while detached.
func (a *App) openBouncerChannels(s *bncState) {
	var rows, keys []string
	for _, c := range s.channels {
		rows = append(rows, fmt.Sprintf("%-22s %-14s %s", c.name, c.network, c.status))
		keys = append(keys, c.key())
	}
	l := &listBox{Title: "Bouncer Channels", Header: fmt.Sprintf("%-22s %-14s %s", "Channel", "Network", "State"),
		Rows: rows, Keys: keys, Buttons: []string{"Options...", "Detach", "Attach", "Close"}}
	if len(rows) == 0 {
		l.Rows = []string{"No channels yet: the bouncer keeps the ones you join."}
	}
	l.OnClose = backToSettings(bncChannels)
	l.OnPress = func(a *App, l *listBox, button, key string) tea.Cmd {
		if key == "" {
			return nil
		}
		switch button {
		case "Detach", "Attach":
			a.serv(a.home, a.home.Status(), false, "channel", "update", key, "-detached", fmt.Sprint(button == "Detach"))
			a.openBouncerSettings(bncChannels)
		case "Options...":
			a.openChannelOptions(key)
		}
		return nil
	}
	a.openDialog(l)
}

// channelChoices are soju's -relay-detached, -reattach-on and -detach-on
// values, in words, with "unchanged" first: soju doesn't say what they
// are now.
var channelChoices = []string{"unchanged", "the default", "nothing", "highlights", "any message"}

var channelValues = map[string]string{"the default": "default", "nothing": "none",
	"highlights": "highlight", "any message": "message"}

// openChannelOptions sets what soju does with a channel (key is
// "#chan/Network") while it's detached.
func (a *App) openChannelOptions(key string) {
	back := func(a *App) tea.Cmd { a.openBouncerSettings(bncChannels); return nil }
	a.openDialog(&formBox{Title: "Channel Options - " + key, Buttons: []string{"OK", "Cancel"}, Compact: true,
		Lines: []string{"While detached, the bouncer stays in the channel.",
			"The bouncer doesn't say what these are now:",
			"unchanged leaves each one as it is."},
		Fields: []*formField{
			newChoice("Tell me about:", channelChoices, 0),
			newChoice("Reattach on:", channelChoices, 0),
			newChoice("Detach on:", channelChoices, 0),
			newField("Detach after:", "", false),
		},
		OnOK: func(a *App, v []string) (tea.Cmd, error) {
			args := []string{"channel", "update", key}
			for i, flag := range []string{"-relay-detached", "-reattach-on", "-detach-on"} {
				if val := channelValues[v[i]]; val != "" {
					args = append(args, flag, val)
				}
			}
			if d := strings.TrimSpace(v[3]); d != "" {
				if !regexp.MustCompile(`^(0|([0-9]+[smh])+)$`).MatchString(d) {
					return nil, errors.New("detach after is a time like 30m or 24h, or 0 for never")
				}
				args = append(args, "-detach-after", d)
			}
			if len(args) > 3 {
				a.serv(a.home, a.home.Status(), false, args...)
			}
			return back(a), nil
		},
		OnCancel: back,
	})
}

// openBouncerPassword changes your bouncer password, and the one trout
// keeps for logging in, if it keeps one.
func (a *App) openBouncerPassword() {
	back := func(a *App) tea.Cmd { a.openBouncerSettings(bncAccount); return nil }
	a.openDialog(&formBox{Title: "Bouncer Password", Buttons: []string{"OK", "Cancel"},
		Lines:  []string{"Your password for the bouncer, " + a.bnc.User + ":", "phones and web browsers log in with it."},
		Fields: []*formField{newField("New password:", "", true), newField("Again:", "", true)},
		OnOK: func(a *App, v []string) (tea.Cmd, error) {
			switch {
			case v[0] == "":
				return nil, errors.New("type the new password")
			case v[0] != v[1]:
				return nil, errors.New("the two don't match")
			case a.control == nil:
				return nil, errors.New("not connected to the bouncer")
			}
			a.serv(a.home, a.home.Status(), false, "user", "update", "-password", v[0])
			if a.cfg != nil {
				if old, err := a.cfg.StoredPassword(a.bnc.Name); err == nil && old != "" {
					if err := a.cfg.SetPassword(a.bnc.Name, v[0]); err != nil {
						a.addLine(a.home.Status(), state.RoleQuit, "* Couldn't save the new password: "+err.Error())
					}
				}
			}
			if a.password != "" {
				a.password = v[0]
			}
			return back(a), nil
		},
		OnCancel: back,
	})
}

// openBouncerUsers is the admin's list of the bouncer's users.
func (a *App) openBouncerUsers(s *bncState) {
	var rows, keys []string
	for _, u := range s.users {
		rows = append(rows, fmt.Sprintf("%-16s %-16s %s", u.name, u.attrs, u.info))
		keys = append(keys, u.name)
	}
	l := &listBox{Title: "Bouncer Users", Header: fmt.Sprintf("%-16s %-16s %s", "User", "", "Networks"),
		Rows: rows, Keys: keys, Buttons: []string{"Edit...", "Add...", "Delete", "Close"}}
	l.OnClose = backToSettings(bncUsers)
	l.OnPress = func(a *App, l *listBox, button, name string) tea.Cmd {
		i := slices.IndexFunc(s.users, func(u userStatus) bool { return u.name == name })
		switch {
		case button == "Add...":
			a.openAddUser()
		case i < 0:
		case strings.EqualFold(name, a.bnc.User):
			// Taking away your own admin, or switching yourself off,
			// locks you out of this list; Account has your password.
			m := message("That's you: Account changes your password,", "and another admin the rest.")
			m.OnClose = func(a *App, _ int) tea.Cmd { a.openBouncerUsers(s); return nil }
			a.openDialog(m)
		case button == "Edit...":
			a.openEditUser(s.users[i])
		case button == "Delete":
			a.deleteUser(name)
		}
		return nil
	}
	a.openDialog(l)
}

// openEditUser changes another user: admin or not, switched off or not,
// and their password.
func (a *App) openEditUser(u userStatus) {
	back := func(a *App) tea.Cmd { a.openBouncerSettings(bncUsers); return nil }
	attrs := strings.Split(u.attrs, ", ")
	admin, enabled := slices.Contains(attrs, "admin"), !slices.Contains(attrs, "disabled")
	a.openDialog(&formBox{Title: "Edit User - " + u.name, Buttons: []string{"OK", "Cancel"}, Compact: true,
		Lines: []string{"An empty password keeps theirs."},
		Fields: []*formField{newCheck("An admin, who can manage users", admin),
			newCheck("Switched on: can log in, and stays on networks", enabled),
			newField("New password:", "", true)},
		OnOK: func(a *App, v []string) (tea.Cmd, error) {
			args := []string{"user", "update", u.name}
			if (v[0] == "1") != admin {
				args = append(args, "-admin", fmt.Sprint(!admin))
			}
			if (v[1] == "1") != enabled {
				args = append(args, "-enabled", fmt.Sprint(!enabled))
			}
			if v[2] != "" {
				args = append(args, "-password", v[2])
			}
			if len(args) > 3 {
				a.serv(a.home, a.home.Status(), false, args...)
			}
			return back(a), nil
		},
		OnCancel: back,
	})
}

func (a *App) openAddUser() {
	back := func(a *App) tea.Cmd { a.openBouncerSettings(bncUsers); return nil }
	a.openDialog(&formBox{Title: "Add User", Buttons: []string{"OK", "Cancel"}, Compact: true,
		Lines: []string{"Another person on this bouncer, with their own", "networks and password."},
		Fields: []*formField{newField("User name:", "", false), newField("Password:", "", true),
			newCheck("An admin, who can manage users", false)},
		OnOK: func(a *App, v []string) (tea.Cmd, error) {
			name := strings.TrimSpace(v[0])
			switch {
			case name == "" || strings.ContainsAny(name, " \t/"):
				return nil, errors.New("a user name is one word")
			case v[1] == "":
				return nil, errors.New("give them a password")
			}
			// "-admin=": user create's -admin is a plain bool flag, which
			// takes no separate value (checked on soju 0.11.1; user
			// update's takes one).
			a.serv(a.home, a.home.Status(), false, "user", "create", "-username", name, "-password", v[1],
				"-admin="+fmt.Sprint(v[2] == "1"))
			return back(a), nil
		},
		OnCancel: back,
	})
}

// deleteToken is soju's answer to "user delete name": the command to send
// to confirm, with its token.
var deleteToken = regexp.MustCompile(`send "user delete (\S+) (\S+)"`)

// deleteUser asks, then deletes a user: soju answers the first "user
// delete" with a token to send back, so a typo can't delete someone.
func (a *App) deleteUser(name string) {
	a.openDialog(&msgBox{Title: "Delete User", Buttons: []string{"Yes", "No"}, Sel: 1, Cancel: 1,
		Lines: []string{"Delete " + name + " from the bouncer?", "",
			"Their networks, channels and history go too."},
		OnClose: func(a *App, button int) tea.Cmd {
			if button != 0 {
				a.openBouncerSettings(bncUsers)
				return nil
			}
			a.servList([]string{"user", "delete", name}, func(lines []string, err string) {
				for _, l := range lines {
					if m := deleteToken.FindStringSubmatch(l); m != nil && m[1] == name {
						a.serv(a.home, a.home.Status(), false, "user", "delete", name, m[2])
						a.openBouncerSettings(bncUsers)
						return
					}
				}
				a.addLine(a.home.Status(), state.RoleQuit, "* Bouncer: "+cmp.Or(err, "couldn't delete "+name))
			})
			return nil
		}})
}

// oneOf fixes soju's "1 channels" and "1 networks".
var oneRe = regexp.MustCompile(`\b1 (channel|network|user|downstream|upstream)s\b`)

func oneOf(s string) string { return oneRe.ReplaceAllString(s, "1 $1") }
