package ui

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MaxRenaud/trout/internal/config"
	"github.com/MaxRenaud/trout/internal/install"
	"github.com/MaxRenaud/trout/internal/local"
	"github.com/MaxRenaud/trout/internal/state"
)

// The This Computer bouncer: trout's own soju (internal/local), started
// when trout connects to it and stopped when trout quits or switches
// away. It stays connected to your networks only while trout is open.

const localName = "This computer"

type localUpMsg struct {
	s   *local.Soju
	err error
}

// bouncerSet reports whether there's a bouncer to connect to.
func (a *App) bouncerSet() bool { return a.bnc.User != "" && (a.bnc.Local || a.bnc.Address != "") }

func (a *App) hasLocal() bool {
	return a.cfg != nil && slices.ContainsFunc(a.cfg.Bouncers, func(b config.Bouncer) bool { return b.Local })
}

// bncAddress is the bouncer's address for people to read.
func (a *App) bncAddress() string {
	if a.bnc.Local {
		return "this computer"
	}
	return a.bnc.Address
}

// localDir is where trout's own soju keeps its database, config and log.
func (a *App) localDir() string {
	if a.cfg != nil {
		return filepath.Join(filepath.Dir(a.cfg.Path), "soju")
	}
	dir, _ := os.UserConfigDir()
	return filepath.Join(dir, "trout", "soju")
}

// useLocal switches to the This Computer bouncer, made the first time.
func (a *App) useLocal() tea.Cmd {
	b := config.Bouncer{Name: localName, Local: true, User: a.defaultAccount()}
	if a.cfg != nil {
		if old := a.cfg.Find(localName); old != nil && old.Local {
			b = *old
		}
	}
	pw := a.bouncerPassword(b)
	if a.cfg != nil {
		if pw == "" {
			pw = install.NewPassword()
		}
		a.cfg.Use(b)
		if err := a.cfg.SetPassword(b.Name, pw); err != nil {
			a.addLine(a.home.Status(), state.RoleQuit, "* Couldn't save the bouncer's password: "+err.Error())
		}
		a.saveConfig()
	}
	return a.switchBouncer(b, pw)
}

// startLocal starts trout's own soju, in the background.
func (a *App) startLocal() tea.Cmd {
	if a.password == "" { // a config.toml whose credentials went missing
		a.password = install.NewPassword()
		if a.cfg != nil {
			a.cfg.SetPassword(a.bnc.Name, a.password)
		}
	}
	a.addLine(a.home.Status(), state.RoleInfo, "* Starting the bouncer on this computer...")
	dir, account, password := a.localDir(), a.bnc.User, a.password
	return func() tea.Msg {
		s, err := local.Start(context.Background(), dir, account, password)
		return localUpMsg{s, err}
	}
}

func (a *App) localUp(msg localUpMsg) tea.Cmd {
	if !a.bnc.Local { // switched away while it started
		msg.s.Stop()
		return nil
	}
	if msg.err != nil {
		a.stopped = true
		a.addLine(a.home.Status(), state.RoleQuit, "* Couldn't start the bouncer on this computer: "+msg.err.Error())
		return nil
	}
	a.local = msg.s
	a.bnc.Address = "irc+insecure://" + msg.s.Addr
	host, port, _ := net.SplitHostPort(msg.s.Addr)
	a.home.Server = host
	a.home.Port, _ = strconv.Atoi(port)
	return a.connectBouncer()
}
