package ui

import (
	"context"
	"net"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MaxRenaud/trout/internal/config"
	"github.com/MaxRenaud/trout/internal/install"
	"github.com/MaxRenaud/trout/internal/irc"
	"github.com/MaxRenaud/trout/internal/state"
)

// A bouncer trout set up has a self-signed certificate: trout pins its
// key, but phones (Goguma) and web browsers have no pin, so they warn.
// Let's Encrypt can vouch for it instead, IP address and all: trout puts
// trout-cert on the server (setup.sh's cert mode), which gets the
// certificate for soju's own key and renews it with a timer, so the pin
// stays good. Asking is the setup wizard's last step and Bouncer > Bouncers
// > Server > Certificate; neither is mIRC's.

// letsEncrypt is config.Bouncer.Cert for a Let's Encrypt certificate.
const letsEncrypt = "letsencrypt"

// subscriberAgreement is where Let's Encrypt keeps the agreement getting
// a certificate accepts (the ACME account trout-cert makes says so).
const subscriberAgreement = "https://letsencrypt.org/repository/"

// bouncerHost is the host in b's address: what the certificate is for.
func bouncerHost(b config.Bouncer) string {
	hostport, _, err := irc.ParseAddress(b.Address)
	if err != nil {
		return ""
	}
	host, _, _ := net.SplitHostPort(hostport)
	return host
}

// certProblem says why b can't have a Let's Encrypt certificate, or "".
func certProblem(b config.Bouncer) string {
	if _, useTLS, err := irc.ParseAddress(b.Address); err != nil || !useTLS {
		return "it doesn't use TLS"
	}
	return install.CertProblem(bouncerHost(b))
}

// openServer is Bouncers > Server, for a bouncer trout set up: what runs
// there, and Update and Certificate.
func (a *App) openServer(b config.Bouncer, back modal) {
	version := "soju (trout didn't save which version)"
	switch {
	case b.SojuVersion == install.SojuVersion:
		version = "soju " + b.SojuVersion + ", the one this trout comes with"
	case b.SojuVersion != "":
		version = "soju " + b.SojuVersion + "; this trout comes with " + install.SojuVersion
	}
	cert := []string{"Certificate:  self-signed: trout knows its key, but phones and",
		"              web browsers warn about it"}
	if b.Cert == letsEncrypt {
		cert = []string{"Certificate:  from Let's Encrypt, renewed on the server"}
	}
	lines := append([]string{b.Name + " runs on " + b.SSH + ", set up by trout.", "",
		"Bouncer:      " + version}, cert...)
	a.openDialog(&msgBox{Title: "Server", Lines: lines, Buttons: []string{"Update", "Certificate", "Close"}, Cancel: 2,
		OnClose: func(a *App, button int) tea.Cmd {
			switch button {
			case 0:
				return a.openReinstall(b, "update")
			case 1:
				a.askCert(b, func(a *App) { a.openServer(b, back) }, false)
				return nil
			}
			if back != nil {
				a.openDialog(back)
			}
			return nil
		}})
}

// askCert asks before getting b a Let's Encrypt certificate (or, when it
// has one, offers to set it up again or go back to the self-signed one).
// Getting one accepts Let's Encrypt's Subscriber Agreement, so it says
// so. No email: Let's Encrypt doesn't keep one with the account, and
// stopped sending expiry reminders in 2025. back, if set, reopens what
// asked. offered is the setup wizard asking: its button is Not Now, which
// says where to find it later.
func (a *App) askCert(b config.Bouncer, back func(*App), offered bool) {
	reopen := func(a *App) {
		if back != nil {
			back(a)
		}
	}
	if why := certProblem(b); why != "" {
		m := message("Let's Encrypt can't vouch for "+b.Name+": "+why+".", "",
			"Phones and web browsers will keep warning about its certificate;",
			"trout knows its key, so trout doesn't.")
		m.Title = "Trusted Certificate"
		m.OnClose = func(a *App, _ int) tea.Cmd { reopen(a); return nil }
		a.openDialog(m)
		return
	}
	if b.Cert == letsEncrypt {
		a.openDialog(&msgBox{Title: "Trusted Certificate", Lines: []string{
			"Let's Encrypt vouches for " + b.Name + "'s certificate, and the server",
			"renews it every few days. trout still checks the key it pinned.",
			"",
			"Set Up Again puts the renewal back in and renews now if it's due;",
			"if the server couldn't renew it, this says why.",
			"Self-Signed goes back to the certificate only trout knows.",
		}, Buttons: []string{"Set Up Again", "Self-Signed", "Cancel"}, Cancel: 2,
			OnClose: func(a *App, button int) tea.Cmd {
				switch button {
				case 0:
					return a.openReinstall(b, "cert")
				case 1:
					return a.openReinstall(b, "nocert")
				}
				reopen(a)
				return nil
			}})
		return
	}
	cancel := "Cancel"
	if offered {
		cancel = "Not Now"
	}
	a.openDialog(&msgBox{Title: "Trusted Certificate", Lines: []string{
		"Phones and web browsers don't know " + b.Name + "'s certificate:",
		"Goguma says Bad Certificate, and browsers warn. Let's Encrypt can",
		"vouch for it instead, for free; the server renews it every few days.",
		"trout still checks the key it pinned.",
		"",
		"Let's Encrypt checks the server over port 80, so port 80 has to be",
		"open to everyone, in the server's firewall and at your provider.",
		"",
		"Getting one means you agree to Let's Encrypt's Subscriber Agreement:",
		subscriberAgreement,
	}, Buttons: []string{"Get Certificate", cancel}, Cancel: 1,
		OnClose: func(a *App, button int) tea.Cmd {
			if button == 0 {
				return a.openReinstall(b, "cert")
			}
			if offered {
				a.addLine(a.home.Status(), state.RoleInfo, "* Bouncer: get "+b.Name+
					" a certificate phones trust any time from Bouncer > Bouncers > Server > Certificate")
			}
			reopen(a)
			return nil
		}})
}

// offerCert asks about a certificate once a new bouncer is set up, when
// Let's Encrypt could vouch for it.
func (a *App) offerCert(name string) {
	a.whenFree(func(a *App) tea.Cmd {
		if a.cfg == nil {
			return nil
		}
		b := a.cfg.Find(name)
		if b == nil || b.Cert != "" || b.SSH == "" || certProblem(*b) != "" {
			return nil
		}
		a.askCert(*b, nil, true)
		return nil
	})
}

// certCheckedMsg is what watchCert found.
type certCheckedMsg struct {
	name string
	cert irc.ServerCert
	err  error
}

// readCert reads a bouncer's certificate; tests fake it.
func (a *App) readCert(ctx context.Context, cfg irc.Config) (irc.ServerCert, error) {
	if a.hooks != nil && a.hooks.readCert != nil {
		return a.hooks.readCert(ctx, cfg)
	}
	return irc.ReadCert(ctx, cfg)
}

// watchCert checks, once connected, that a bouncer with a Let's Encrypt
// certificate still shows one that's trusted and not about to run out.
// If the server stopped renewing it, trout still connects (it pins the
// key), so nothing else would tell you before phones did.
func (a *App) watchCert() tea.Cmd {
	b := a.savedBouncer()
	if b == nil || b.Cert != letsEncrypt || a.certWatched[b.Name] {
		return nil
	}
	if a.certWatched == nil {
		a.certWatched = map[string]bool{}
	}
	a.certWatched[b.Name] = true // once a session, not every reconnect
	addr, _, err := irc.ParseAddress(b.Address)
	if err != nil {
		return nil
	}
	cfg := irc.Config{Addr: addr, TLS: true, Pin: b.Pin, Dial: a.dialer(false)}
	name := b.Name
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		sc, err := a.readCert(ctx, cfg)
		return certCheckedMsg{name, sc, err}
	}
}

// certChecked warns in the status window when the certificate needs
// looking at; a failed connection says so elsewhere.
func (a *App) certChecked(m certCheckedMsg) {
	if m.err != nil {
		return
	}
	fix := "the server couldn't renew it. Bouncer > Bouncers > Server > Certificate > Set Up Again tries now and says why."
	switch left := time.Until(m.cert.NotAfter); {
	case !m.cert.Trusted:
		a.addLine(a.home.Status(), state.RoleQuit, "* Bouncer: phones and web browsers won't trust "+m.name+"'s certificate "+
			"("+m.cert.Why.Error()+"); "+fix)
	case left < 48*time.Hour:
		a.addLine(a.home.Status(), state.RoleQuit, "* Bouncer: "+m.name+"'s certificate runs out "+
			m.cert.NotAfter.Local().Format("Mon Jan 2 15:04")+", and "+fix)
	}
}
