package ui

import (
	"regexp"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MaxRenaud/trout/internal/state"
)

// A network whose certificate the bouncer can't check (self-signed, like
// most of EFnet's, or expired): trout asks whether to trust it and pins
// it with soju's network update -certfp. Like mIRC, which warns about an
// invalid certificate and remembers the ones you accept (its help has
// /sockopen's -w "display warning dialog" and -p "prevent certificate
// caching"); unlike it, there's no accepting just once, since soju keeps
// a pin until it's cleared.
//
// soju only says a server's fingerprint when a pinned one doesn't match,
// so trout pins a placeholder, reads the real one from the error, and
// clears the placeholder at once: the question waits with soju as it
// was. (If trout quits during that second, the next run sees a mismatch
// and asks as for a changed certificate, which Trust New Key settles.)

var (
	certFailRe     = regexp.MustCompile(`tls: failed to verify certificate: (?:x509: )?(.*)`)
	certMismatchRe = regexp.MustCompile(`fingerprint doesn't match the server's - ([0-9a-f]{128})`)
	certProbe      = strings.Repeat("0", 64) // a SHA-256 fingerprint no server has
)

type certStage int

const (
	certProbing  certStage = iota + 1 // the placeholder's pinned; the fingerprint's coming
	certAsking                        // the question is open, or waiting to open
	certDeclined                      // Cancel: not asked again until the network's edited or trout restarts
)

type netCert struct {
	stage certStage
	why   string // what the bouncer said was wrong with it
}

// networkError looks at an error soju reports for n's connection, for a
// certificate it couldn't check or one that doesn't match the pin.
func (a *App) networkError(n *state.Network, e string) {
	c := a.netCerts[n]
	if m := certMismatchRe.FindStringSubmatch(e); m != nil {
		switch {
		case c != nil && c.stage == certProbing:
			c.stage = certAsking
			a.serv(a.home, n.Status(), true, "network", "update", n.Name, "-certfp", "")
			why, fp := c.why, m[1]
			a.whenFree(func(a *App) tea.Cmd { a.openDialog(a.netCertBox(n, why, fp)); return nil })
		case c == nil:
			a.netCerts[n] = &netCert{stage: certAsking}
			fp := m[1]
			a.whenFree(func(a *App) tea.Cmd { a.openDialog(a.netCertChangedBox(n, fp)); return nil })
		}
		return
	}
	if m := certFailRe.FindStringSubmatch(e); m != nil && c == nil {
		a.netCerts[n] = &netCert{stage: certProbing, why: m[1]}
		a.addLine(n.Status(), state.RoleInfo, "* Bouncer: can't check "+n.Name+"'s certificate ("+m[1]+
			"); asking for its fingerprint...")
		a.serv(a.home, n.Status(), true, "network", "update", n.Name, "-certfp", certProbe)
	}
}

// fingerprintLines is a SHA-512 fingerprint's line in a dialog, which the
// message box cuts into even pieces that fit: two at most sizes.
func fingerprintLines(fp string) []string {
	return []string{"    " + fp}
}

func (a *App) netCertBox(n *state.Network, why, fp string) *msgBox {
	lines := []string{
		"The bouncer can't check the certificate " + n.Name + " sent:",
		"    " + why,
		"That's common on IRC networks, many of which sign their own.",
		"",
		"Its fingerprint (SHA-512) is",
	}
	lines = append(lines, fingerprintLines(fp)...)
	lines = append(lines, "",
		"Trust it? The bouncer will remember it and refuse to",
		"connect if "+n.Name+"'s certificate ever changes.")
	d := message(lines...)
	d.Title = "Unknown Certificate"
	// Cancel has the focus, so a stray Enter doesn't trust a certificate.
	d.Buttons, d.Sel, d.Cancel = []string{"Trust", "Cancel"}, 1, 1
	d.OnClose = func(a *App, button int) tea.Cmd { return a.trustNetCert(n, button == 0, fp) }
	return d
}

func (a *App) netCertChangedBox(n *state.Network, fp string) *msgBox {
	lines := []string{
		"WARNING: " + n.Name + "'s certificate has changed!",
		"",
		"It doesn't match the one the bouncer trusts. It sent",
	}
	lines = append(lines, fingerprintLines(fp)...)
	lines = append(lines, "",
		"Someone could be intercepting the connection, or the",
		"network has a new certificate. Only trust the new one",
		"if you know the network's certificate was changed.")
	d := message(lines...)
	d.Title = "Certificate Changed"
	// Cancel has the focus: Enter never accepts a changed certificate.
	d.Buttons, d.Sel, d.Cancel = []string{"Trust New Certificate", "Cancel"}, 1, 1
	d.OnClose = func(a *App, button int) tea.Cmd { return a.trustNetCert(n, button == 0, fp) }
	return d
}

// trustNetCert pins fp for n, which reconnects it, or leaves n as it is
// and stops asking.
func (a *App) trustNetCert(n *state.Network, trust bool, fp string) tea.Cmd {
	if !trust {
		a.netCerts[n] = &netCert{stage: certDeclined}
		a.addLine(n.Status(), state.RoleInfo, "* Not trusting "+n.Name+"'s certificate. Edit it in File > "+
			"Select Server to be asked again, or to connect with irc+insecure://")
		return nil
	}
	delete(a.netCerts, n)
	a.serv(a.home, n.Status(), false, "network", "update", n.Name, "-certfp", fp)
	return nil
}
