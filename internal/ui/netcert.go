package ui

import (
	"cmp"
	"context"
	"crypto/sha512"
	"crypto/tls"
	"encoding/hex"
	"net"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

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
	certPinning                       // trusted; trout's looking for which of the network's servers it was
)

type netCert struct {
	stage certStage
	why   string // what the bouncer said was wrong with it
}

// networkError looks at an error soju reports for n's connection, for a
// certificate it couldn't check or one that doesn't match the pin.
func (a *App) networkError(n *state.Network, e string) {
	c := a.netCerts[n]
	if c != nil && c.stage == certPinning {
		return // soju reconnecting to another of a round robin's servers meanwhile
	}
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
	a.netCerts[n] = &netCert{stage: certPinning}
	a.serv(a.home, n.Status(), false, "network", "update", n.Name, "-certfp", fp)
	host, port := n.Attrs["host"], cmp.Or(n.Attrs["port"], "6697")
	find := a.certServers
	return func() tea.Msg {
		server, servers := find(host, port, fp)
		return certServerMsg{n, fp, port, server, servers}
	}
}

// A round robin (irc.efnet.net is 18 servers) whose servers each have
// their own certificate can't be pinned: soju keeps one fingerprint, and
// its next connection lands on another server. So once you trust one,
// trout looks at the name's servers itself, and if their certificates
// differ, points the network at the one you trusted.

// certServerMsg is what trout found: the server with the trusted
// certificate ("" if none), and how many different certificates the
// name's servers that answered had.
type certServerMsg struct {
	n       *state.Network
	fp      string
	port    string
	server  string
	servers int
}

// certServers dials each of host's addresses and returns the one whose
// certificate has fingerprint fp, by a name in that certificate that
// leads to it if there is one, and how many different certificates
// there were.
func certServers(host, port, fp string) (string, int) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ips, err := net.DefaultResolver.LookupHost(ctx, host)
	if err != nil || len(ips) < 2 {
		return "", len(ips)
	}
	var (
		mu    sync.Mutex
		fps   = map[string]bool{}
		found string
		names []string
		wg    sync.WaitGroup
	)
	d := tls.Dialer{Config: &tls.Config{InsecureSkipVerify: true, ServerName: host}, NetDialer: &net.Dialer{}}
	for _, ip := range ips {
		wg.Go(func() {
			c, err := d.DialContext(ctx, "tcp", net.JoinHostPort(ip, port))
			if err != nil {
				return
			}
			defer c.Close()
			certs := c.(*tls.Conn).ConnectionState().PeerCertificates
			if len(certs) == 0 {
				return
			}
			sum := sha512.Sum512(certs[0].Raw)
			mu.Lock()
			defer mu.Unlock()
			fps[hex.EncodeToString(sum[:])] = true
			if hex.EncodeToString(sum[:]) == fp {
				found, names = ip, append(slices.Clone(certs[0].DNSNames), certs[0].Subject.CommonName)
			}
		})
	}
	wg.Wait()
	if found == "" || len(fps) < 2 {
		return "", len(fps)
	}
	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second) // the dials' may be spent
	defer cancel()
	for _, name := range names {
		if addrs, err := net.DefaultResolver.LookupHost(ctx, name); err == nil && slices.Contains(addrs, found) {
			return name, len(fps)
		}
	}
	return found, len(fps)
}

// certServerFound points n at the server with the certificate you
// trusted, when its name is a round robin of servers with different
// ones.
func (a *App) certServerFound(m certServerMsg) {
	n := m.n
	if c := a.netCerts[n]; c == nil || c.stage != certPinning {
		return // edited or asked about again meanwhile
	}
	delete(a.netCerts, n)
	if m.servers < 2 {
		return
	}
	host := n.Attrs["host"]
	if m.server == "" {
		// The bouncer reached a server trout can't (or its DNS answers
		// differently): say so rather than asking about each in turn.
		a.netCerts[n] = &netCert{stage: certDeclined}
		a.addLine(n.Status(), state.RoleInfo, "* "+host+" is several servers, each with its own certificate, and "+
			"the bouncer can trust only one. Pick one of "+n.Name+"'s servers in File > Select Server.")
		return
	}
	a.addLine(n.Status(), state.RoleInfo, "* "+host+" is several servers, each with its own certificate, and "+
		"the bouncer can trust only one: "+n.Name+" now connects to "+m.server+", the one you trusted.")
	a.serv(a.home, n.Status(), false, "network", "update", n.Name,
		"-addr", "ircs://"+net.JoinHostPort(m.server, m.port), "-certfp", m.fp)
}
