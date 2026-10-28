package ui

import (
	"cmp"
	"context"
	"errors"
	"net"
	"slices"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MaxRenaud/trout/internal/config"
	"github.com/MaxRenaud/trout/internal/proxy"
)

// Options > Connect > Proxy, like mIRC's: SOCKS4, SOCKS5 or an HTTP
// proxy, for the link to the bouncer (and its file host), for DCC Gets
// and Chats, or both. mIRC's proxy is for IRC servers; here that's the
// bouncer, which talks to the servers itself.

var proxyTypes = []string{"", "socks4", "socks5", "http"}

// dialer is how to reach the bouncer (dcc false) or a DCC sender.
func (a *App) dialer(dcc bool) func(ctx context.Context, network, addr string) (net.Conn, error) {
	p := a.book().Proxy
	if p.Type == "" || p.Host == "" || (dcc && !p.DCC) || (!dcc && !p.Bouncer) {
		return (&net.Dialer{}).DialContext
	}
	d := &proxy.Dialer{Type: p.Type, Addr: net.JoinHostPort(p.Host, strconv.Itoa(cmp.Or(p.Port, 1080))),
		User: p.User, Password: a.proxyPassword(), Except: p.Except}
	return d.DialContext
}

func (a *App) proxyPassword() string {
	if a.cfg == nil {
		return a.proxyPass
	}
	pw, _ := a.cfg.StoredPassword(config.ProxyPassword)
	return pw
}

func (a *App) openProxy() {
	p := a.book().Proxy
	port := ""
	if p.Port != 0 {
		port = strconv.Itoa(p.Port)
	}
	f := &formBox{Title: "Proxy", Buttons: []string{"OK", "Cancel"}, Compact: true,
		Lines: []string{"Connect through a proxy, like an SSH tunnel (ssh -D)."},
		Fields: []*formField{
			newRadio("type", "None", p.Type == ""),
			newRadio("type", "SOCKS4", p.Type == "socks4"),
			newRadio("type", "SOCKS5", p.Type == "socks5"),
			newRadio("type", "HTTP (CONNECT)", p.Type == "http"),
			newField("Host:", p.Host, false),
			newField("Port:", cmp.Or(port, "1080"), false),
			newField("User:", p.User, false),
			newField("Password:", a.proxyPassword(), true),
			newCheck("For the bouncer (and its file host)", p.Bouncer || p.Type == ""),
			newCheck("For DCC Gets and Chats", p.DCC || p.Type == ""),
			newField("Not for:", strings.Join(p.Except, " "), false),
		}}
	f.OnOK = func(a *App, v []string) (tea.Cmd, error) {
		np := config.Proxy{Host: strings.TrimSpace(v[4]), User: strings.TrimSpace(v[6]),
			Bouncer: v[8] == "1", DCC: v[9] == "1", Except: strings.Fields(v[10])}
		np.Type = proxyTypes[max(0, slices.Index(v[:4], "1"))]
		if np.Type != "" {
			port, err := strconv.Atoi(strings.TrimSpace(v[5]))
			if err != nil || port < 1 || port > 65535 {
				return nil, errors.New("the port is a number, usually 1080")
			}
			if np.Host == "" {
				return nil, errors.New("type the proxy's host")
			}
			np.Port = port
		}
		a.book().Proxy = np
		if a.cfg == nil {
			a.proxyPass = v[7]
		} else if err := a.cfg.SetPassword(config.ProxyPassword, v[7]); err != nil {
			return nil, err
		}
		a.saveConfig()
		a.openOptions(optConnect)
		return nil, nil
	}
	f.OnCancel = func(a *App) tea.Cmd { a.openOptions(optConnect); return nil }
	a.openDialog(f)
}

// proxyLabel is the Connect page's line for the proxy.
func (a *App) proxyLabel() string {
	p := a.book().Proxy
	if p.Type == "" {
		return "Proxy: none..."
	}
	return "Proxy: " + strings.ToUpper(p.Type) + " " + p.Host + "..."
}
