package ui

import (
	"context"
	"net/url"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"rsc.io/qr"

	"github.com/MaxRenaud/trout/internal/irc"
)

// phoneBox is Tools > Connect a Phone: a QR code that opens Goguma, the
// phone IRC client that speaks soju's extensions and push notifications,
// with the bouncer in use filled in (an ircs:// link, which Goguma
// registers on iOS and Android). Goguma asks before trusting a certificate
// no authority signed, showing its SHA-1; for a pinned bouncer this shows
// the same, to compare, which trout gets by connecting with its pin.
type phoneBox struct {
	withPassword bool
	sha1         string // the certificate's, once known; "" when no pin
	checking     bool
	err          string
}

type phoneSHA1Msg struct {
	box  *phoneBox
	sha1 string
	err  error
}

// phoneOK says whether a phone could reach the bouncer in use: not This
// Computer, and over TLS.
func (a *App) phoneOK() bool {
	_, useTLS, err := irc.ParseAddress(a.bnc.Address)
	return !a.bnc.Local && err == nil && useTLS && a.bnc.User != ""
}

func (a *App) openPhone() tea.Cmd {
	d := &phoneBox{withPassword: true}
	a.openDialog(d)
	if a.bnc.Pin == "" {
		return nil
	}
	d.checking = true
	addr, _, _ := irc.ParseAddress(a.bnc.Address)
	cfg := irc.Config{Addr: addr, TLS: true, Pin: a.bnc.Pin, Dial: a.dialer(false)}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		sum, err := irc.CertSHA1(ctx, cfg)
		return phoneSHA1Msg{d, sum, err}
	}
}

func (a *App) phoneSHA1(m phoneSHA1Msg) {
	m.box.checking = false
	if m.err != nil {
		m.box.err = m.err.Error()
		return
	}
	m.box.sha1 = m.sha1
}

// link is the bouncer as an IRC URL: ircs://user[:password]@host:port.
func (d *phoneBox) link(a *App) string {
	hostport, _, _ := irc.ParseAddress(a.bnc.Address)
	u := url.User(a.bnc.User)
	if d.withPassword && a.password != "" {
		u = url.UserPassword(a.bnc.User, a.password)
	}
	return "ircs://" + u.String() + "@" + hostport
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
	text("It opens Goguma, an IRC client for soju with push notifications: "+
		"App Store on iPhone, F-Droid or Google Play on Android.", face)
	row++
	switch {
	case d.checking:
		text("Getting the bouncer's certificate...", face)
	case d.err != "":
		text("Couldn't get the bouncer's certificate: "+d.err, face)
	case d.sha1 != "":
		text("Goguma says Bad Certificate the first time. Accept Always if its SHA1 is:", face)
		text(d.sha1, bold)
	}
	row++
	if d.withPassword {
		text("The code has your bouncer password in it.", face)
	} else {
		text("Goguma will ask for your bouncer password.", face)
	}
	label := "Leave Out Password"
	if !d.withPassword {
		label = "Put In Password"
	}
	a.dlgButtons(c, x, y+h-3, w, []string{"Close", label}, 0)
}

func (d *phoneBox) press(a *App, i int) tea.Cmd {
	if i == 1 {
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
	case "p", "l":
		return d.press(a, 1)
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
