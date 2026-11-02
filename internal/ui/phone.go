package ui

import (
	"context"
	"net"
	"net/url"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"rsc.io/qr"

	"github.com/MaxRenaud/trout/internal/irc"
)

// phoneBox is Tools > Connect a Phone: a QR code that opens Goguma, the
// phone IRC client that speaks soju's extensions and push notifications,
// with the bouncer in use filled in: an ircs:// link, which Goguma
// registers on iOS and Android, behind gogumaPage. Goguma asks before trusting a certificate
// no authority signed, showing its SHA-1; for a pinned bouncer this shows
// the same, to compare, which trout gets by connecting with its pin. A
// bouncer with a Let's Encrypt certificate needs none of that.
type phoneBox struct {
	withPassword bool
	web          bool           // the code is gamja's page in a browser, for a trusted bouncer
	cert         irc.ServerCert // once read
	known        bool
	checking     bool
	err          string
}

type phoneCertMsg struct {
	box  *phoneBox
	cert irc.ServerCert
	err  error
}

// phoneProblem says why a phone couldn't use the bouncer in use, one line
// a message, or nil when it could: not This Computer, and over TLS. The
// menu item stays enabled and says this, rather than greying out with no
// reason given (Connect a Phone isn't mIRC's).
func (a *App) phoneProblem() []string {
	_, useTLS, err := irc.ParseAddress(a.bnc.Address)
	switch {
	case a.bnc.Local:
		return []string{"The This Computer bouncer runs only while trout is open,",
			"on this computer: a phone can't reach it.",
			"", "Put a bouncer on a server with Tools > Set Up Bouncer."}
	case err != nil:
		return []string{"The bouncer's address doesn't work: " + err.Error()}
	case !useTLS:
		return []string{"The bouncer doesn't use TLS (its address is " + a.bnc.Address + "),",
			"so your password would cross the internet unencrypted.",
			"", "A phone needs an ircs:// bouncer."}
	case a.bnc.User == "":
		return []string{"The bouncer has no user name to put in the code."}
	}
	return nil
}

func (a *App) openPhone() tea.Cmd {
	if lines := a.phoneProblem(); lines != nil {
		m := message(lines...)
		m.Title = "Connect a Phone"
		a.openDialog(m)
		return nil
	}
	d := &phoneBox{withPassword: true, checking: true}
	a.openDialog(d)
	addr, _, _ := irc.ParseAddress(a.bnc.Address)
	cfg := irc.Config{Addr: addr, TLS: true, Pin: a.bnc.Pin, Dial: a.dialer(false)}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		sc, err := a.readCert(ctx, cfg)
		return phoneCertMsg{d, sc, err}
	}
}

func (a *App) phoneCert(m phoneCertMsg) {
	m.box.checking = false
	if m.err != nil {
		m.box.err = m.err.Error()
		return
	}
	m.box.cert, m.box.known = m.cert, true
}

// webPage is gamja, soju's web client, built unchanged on GitHub Pages
// (github.com/trout-irc/trout-irc.github.io); setup.sh's http-origin lets
// it use the bouncer's WebSocket on soju's https port.
const webPage = "https://trout-irc.github.io/"

// webLink is gamja's page for the bouncer: ?server= its WebSocket, with
// the user name filled in. Never the password: gamja takes none in links.
func webLink(a *App) string {
	hostport, _, _ := irc.ParseAddress(a.bnc.Address)
	host, _, err := net.SplitHostPort(hostport)
	if err != nil {
		host = hostport
	}
	// : and / can stay as they are in a query, so it's readable to type.
	return webPage + "?server=wss://" + net.JoinHostPort(host, "8443") + "/socket&nick=" + url.QueryEscape(a.bnc.User)
}

// gogumaPage is trout's page beside gamja's (goguma.html in the site's
// repo): a button that opens the ircs:// link after the #, and where to
// get Goguma. A phone's camera opens any https link, but shows nothing
// for ircs:// without Goguma installed (an iPhone says "No usable data
// found"). Browsers never send what follows the # to a server.
const gogumaPage = webPage + "goguma#"

// ircLink is the bouncer as an IRC URL: ircs://user[:password]@host:port.
func (d *phoneBox) ircLink(a *App) string {
	hostport, _, _ := irc.ParseAddress(a.bnc.Address)
	u := url.User(a.bnc.User)
	if d.withPassword && a.password != "" {
		u = url.UserPassword(a.bnc.User, a.password)
	}
	return "ircs://" + u.String() + "@" + hostport
}

// link is what the code holds: the IRC link on the Goguma page, or
// gamja's page.
func (d *phoneBox) link(a *App) string {
	if d.web {
		return webLink(a)
	}
	return gogumaPage + d.ircLink(a)
}

func (d *phoneBox) title() string { return "Connect a Phone" }

func (d *phoneBox) code(a *App) *qr.Code {
	c, err := qr.Encode(d.link(a), qr.L)
	if err != nil {
		return nil
	}
	return c
}

// quiet is the white margin around the code, in modules; scanners want
// some, and the terminal's own background may be dark.
const quiet = 2

func (d *phoneBox) size(a *App) (int, int) {
	n := 21
	if c := d.code(a); c != nil {
		n = c.Size
	}
	side := n + 2*quiet
	return min(a.w, side+52), min(a.h, max(side/2+1, 18)+6)
}

func (d *phoneBox) draw(a *App, c *Canvas, x, y, w, h int) {
	t := a.theme()
	face := Style{FG: t.FaceText, BG: t.Face}
	bold := Style{FG: t.FaceText, BG: t.Face, Bold: true}
	code := d.code(a)
	side := 0
	if code != nil {
		// Two modules a cell, upper half block: its foreground is the top
		// module and its background the bottom one. Always black on white.
		side = code.Size + 2*quiet
		black := func(mx, my int) bool {
			mx, my = mx-quiet, my-quiet
			return mx >= 0 && my >= 0 && mx < code.Size && my < code.Size && code.Black(mx, my)
		}
		color := func(b bool) RGB {
			if b {
				return 0x000000
			}
			return 0xffffff
		}
		for cy := 0; cy*2 < side; cy++ {
			for mx := range side {
				c.Set(x+2+mx, y+2+cy, '▀', Style{FG: color(black(mx, cy*2)), BG: color(cy*2+1 < side && black(mx, cy*2+1))})
			}
		}
	}
	tx := x + 2 + side + 3
	tw := x + w - 2 - tx
	row := y + 2
	text := func(s string, st Style) {
		for _, l := range wrapPlain(s, tw) {
			if row < y+h-4 {
				c.Text(tx, row, tw, l, st)
			}
			row++
		}
	}
	text("Scan it with your phone's camera.", bold)
	if d.web {
		text("It opens gamja, soju's web client, in the browser. On any computer, go to:", face)
		text(d.link(a), bold)
		row++
		text("It asks for your bouncer password. If it can't connect, update the bouncer "+
			"(Tools > Bouncers > Server > Update).", face)
		a.dlgButtons(c, x, y+h-3, w, d.buttonLabels(a), 0)
		return
	}
	text("It opens a page with a button for Goguma, an IRC client for soju with push notifications "+
		"(App Store on iPhone, Google Play or F-Droid on Android), and links to get it.", face)
	row++
	switch {
	case d.checking:
		text("Getting the bouncer's certificate...", face)
	case d.err != "":
		text("Couldn't get the bouncer's certificate: "+d.err, face)
	case d.known && d.cert.Trusted:
		text("Goguma trusts the bouncer's certificate ("+d.cert.Issuer+" vouches for it).", face)
	case d.known:
		text("Goguma says Bad Certificate the first time. Accept Always if its SHA1 is:", face)
		text(d.cert.SHA1, bold)
		if b := a.bnc; b.SSH != "" && b.Cert == "" && certProblem(b) == "" {
			row++
			text("Or get one Goguma trusts: Tools > Bouncers > Server > Certificate.", face)
		}
	}
	row++
	if d.withPassword {
		text("The code has your bouncer password in it.", face)
	} else {
		text("Goguma will ask for your bouncer password.", face)
	}
	a.dlgButtons(c, x, y+h-3, w, d.buttonLabels(a), 0)
}

func (d *phoneBox) buttonLabels(*App) []string {
	pw := "Put In Password"
	if d.withPassword {
		pw = "Leave Out Password"
	}
	switch {
	case d.web:
		return []string{"Close", "Goguma"}
	case d.known && d.cert.Trusted: // browsers trust it too: gamja works
		return []string{"Close", pw, "Browser"}
	}
	return []string{"Close", pw}
}

func (d *phoneBox) pressButton(a *App, i int) tea.Cmd { return d.press(a, i) }
func (d *phoneBox) typing() bool                      { return false }

func (d *phoneBox) press(a *App, i int) tea.Cmd {
	switch {
	case i == 1 && d.web, i == 2:
		d.web = !d.web
		return nil
	case i == 1:
		d.withPassword = !d.withPassword
		return nil
	}
	a.dlg = nil
	return nil
}

func (d *phoneBox) key(a *App, k string) tea.Cmd {
	switch k {
	case "enter", "esc":
		return d.press(a, 0)
	}
	return nil
}

func (d *phoneBox) mouse(a *App, m tea.MouseMsg, r Region, double bool) tea.Cmd {
	if m.Action != tea.MouseActionPress || m.Button != tea.MouseButtonLeft {
		return nil
	}
	switch r.S {
	case "button":
		return d.press(a, r.I)
	case "close":
		a.dlg = nil
	}
	return nil
}
