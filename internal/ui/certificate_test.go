package ui

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MaxRenaud/trout/internal/config"
	"github.com/MaxRenaud/trout/internal/irc"
)

// dialogText is what d shows, as plain text.
func dialogText(a *App, d interface {
	size(*App) (int, int)
	draw(*App, *Canvas, int, int, int, int)
}) string {
	w, h := d.size(a)
	c := NewCanvas(w, h, Style{})
	d.draw(a, c, 0, 0, w, h)
	var b strings.Builder
	for y := range c.H {
		for _, cl := range c.cells[y*c.W : (y+1)*c.W] {
			b.WriteRune(max(cl.R, ' '))
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func certBox(t *testing.T, a *App) *msgBox {
	t.Helper()
	box, ok := a.dlg.(*msgBox)
	if !ok || box.Title != "Trusted Certificate" {
		t.Fatalf("the dialog is %T %+v, not Trusted Certificate", a.dlg, a.dlg)
	}
	return box
}

// TestCertWizard: Bouncers > Server > Certificate asks (Let's Encrypt's
// agreement and port 80 in the text), then puts trout-cert on the server
// with setup.sh's cert mode and saves that the bouncer has one; asking
// again offers Self-Signed, which takes it away.
func TestCertWizard(t *testing.T) {
	srv := &fakeServer{sudo: "nopasswd"}
	a, _ := installApp(t, srv)
	b := config.Bouncer{Name: "vps", Address: "ircs://203.0.113.9:6697", User: "max", Pin: "SHA256:x", SSH: "max@vps",
		SojuVersion: "v0.11.1"}
	a.cfg.Use(b)
	a.openSavedBouncers()
	d := a.dlg.(*savedBouncersBox)
	d.sel = slices.IndexFunc(a.savedBouncers(), func(o config.Bouncer) bool { return o.Name == "vps" })
	d.press(a, "Server")
	server, ok := a.dlg.(*msgBox)
	if !ok || server.Title != "Server" || !slices.Equal(server.Buttons, []string{"Update", "Certificate", "Close"}) ||
		!strings.Contains(strings.Join(server.Lines, "\n"), "self-signed") {
		t.Fatalf("Server: %T %+v", a.dlg, a.dlg)
	}
	drive(a, server.close(a, 1)) // Certificate
	box := certBox(t, a)
	text := strings.Join(box.Lines, "\n")
	for _, want := range []string{"port 80", "Subscriber Agreement", "https://letsencrypt.org/repository/", "Goguma says Bad Certificate"} {
		if !strings.Contains(text, want) {
			t.Errorf("the question has no %q:\n%s", want, text)
		}
	}
	drive(a, box.close(a, 1)) // Cancel: back to Server
	if s, ok := a.dlg.(*msgBox); !ok || s.Title != "Server" {
		t.Fatalf("Cancel went to %T", a.dlg)
	}

	drive(a, a.dlg.(*msgBox).close(a, 1))
	drive(a, certBox(t, a).close(a, 0)) // Get Certificate
	w := wizard(t, a)
	if w.mode != "cert" || w.step != instDone || w.title() != "Trusted Certificate" {
		t.Fatalf("mode %q step %d err %q log %q", w.mode, w.step, w.err, w.log)
	}
	ran := srv.lines()
	if !slices.Contains(ran, "sudo -n sh /home/max/.trout-install/setup.sh cert") ||
		!slices.Contains(ran, "cat > /home/max/.trout-install/trout-cert && chmod 755 /home/max/.trout-install/trout-cert") ||
		slices.ContainsFunc(ran, func(l string) bool { return strings.Contains(l, ".trout-install/soju ") }) {
		t.Fatalf("ran %q", ran)
	}
	if !strings.Contains(w.in.Script(), "TROUT_HOST=203.0.113.9\n") { // the bouncer's address, not ssh's name for it
		t.Errorf("the certificate is for the wrong host:\n%s", w.in.Script()[:200])
	}
	a.Update(tea.KeyMsg{Type: tea.KeyEnter}) // Finish
	if got := a.cfg.Find("vps").Cert; got != letsEncrypt {
		t.Fatalf("saved Cert %q", got)
	}
	if saved, _ := config.Load(a.cfg.Path); saved.Find("vps").Cert != letsEncrypt {
		t.Fatal("it isn't in config.toml")
	}
	if !slices.Contains(lineTexts(a.home.Status()), "* Bouncer: vps has a certificate from Let's Encrypt now: phones and web browsers trust it") {
		t.Errorf("status: %q", lineTexts(a.home.Status()))
	}
	if rows := bouncerDetails(a, *a.cfg.Find("vps")); !slices.Contains(rows, [2]string{"TLS key", "SHA256:x, and Let's Encrypt vouches for it"}) {
		t.Errorf("Bouncers says %q", rows)
	}

	// Again: Set Up Again or Self-Signed.
	a.openServer(*a.cfg.Find("vps"), nil)
	if s := a.dlg.(*msgBox); !strings.Contains(strings.Join(s.Lines, "\n"), "from Let's Encrypt, renewed on the server") {
		t.Fatalf("Server says %q", s.Lines)
	}
	drive(a, a.dlg.(*msgBox).close(a, 1))
	box = certBox(t, a)
	if !slices.Equal(box.Buttons, []string{"Set Up Again", "Self-Signed", "Cancel"}) {
		t.Fatalf("with a certificate: %q", box.Buttons)
	}
	drive(a, box.close(a, 1)) // Self-Signed
	if w := wizard(t, a); w.mode != "nocert" || w.step != instDone ||
		!slices.Contains(srv.lines(), "sudo -n sh /home/max/.trout-install/setup.sh nocert") {
		t.Fatalf("mode %q step %d err %q ran %q", w.mode, w.step, w.err, srv.lines())
	}
	a.Update(tea.KeyMsg{Type: tea.KeyEnter}) // Finish
	if got := a.cfg.Find("vps").Cert; got != "" {
		t.Fatalf("still Cert %q", got)
	}
}

// TestCertWizardClosed: closing the finished wizard with its × saves
// what it did, like Finish.
func TestCertWizardClosed(t *testing.T) {
	a, _ := installApp(t, &fakeServer{sudo: "nopasswd"})
	b := config.Bouncer{Name: "vps", Address: "ircs://203.0.113.9:6697", User: "max", Pin: "SHA256:x", SSH: "vps"}
	a.cfg.Use(b)
	drive(a, a.openReinstall(b, "cert"))
	w := wizard(t, a)
	if w.step != instDone {
		t.Fatalf("step %d, err %q", w.step, w.err)
	}
	drive(a, w.leave(a)) // its ×
	if a.dlg != nil || a.cfg.Find("vps").Cert != letsEncrypt {
		t.Fatalf("closed: %T open, Cert %q", a.dlg, a.cfg.Find("vps").Cert)
	}
}

// TestCertNotPossible: Let's Encrypt only vouches for public addresses,
// so a bouncer on a home network is told why, and isn't offered one
// after setting up.
func TestCertNotPossible(t *testing.T) {
	a, _ := installApp(t, &fakeServer{sudo: "nopasswd"})
	for addr, want := range map[string]string{
		"ircs://192.168.1.20:6697":        "private address",
		"ircs://pi.local:6697":            "only your own network knows",
		"irc+insecure://203.0.113.9:6667": "doesn't use TLS",
	} {
		b := config.Bouncer{Name: "home", Address: addr, User: "max", SSH: "pi"}
		a.cfg.Use(b)
		a.askCert(b, nil, false)
		m, ok := a.dlg.(*msgBox)
		if !ok || !strings.Contains(strings.Join(m.Lines, " "), want) || len(m.Buttons) != 1 {
			t.Errorf("%s: %T %+v", addr, a.dlg, a.dlg)
		}
		a.dlg = nil
		a.offerCert("home")
		if a.dlg != nil {
			t.Errorf("%s: offered one after setting up", addr)
		}
	}
}

// TestWatchCert: connecting to a bouncer with a Let's Encrypt certificate
// checks it's still trusted and not about to run out; if the server
// stopped renewing it, the status window says what to do.
func TestWatchCert(t *testing.T) {
	a, _ := installApp(t, &fakeServer{})
	var got irc.ServerCert
	var gotErr error
	a.hooks.readCert = func(context.Context, irc.Config) (irc.ServerCert, error) { return got, gotErr }
	b := config.Bouncer{Name: "vps", Address: "ircs://203.0.113.9:6697", User: "max", Pin: "SHA256:x", SSH: "vps"}
	a.cfg.Use(b)
	a.bnc = b
	if a.watchCert() != nil {
		t.Fatal("watched a self-signed bouncer's certificate")
	}
	a.cfg.Find("vps").Cert = letsEncrypt
	warnings := func() []string {
		var w []string
		for _, l := range lineTexts(a.home.Status()) {
			if strings.Contains(l, "Set Up Again") {
				w = append(w, l)
			}
		}
		return w
	}
	for _, c := range []struct {
		what string
		cert irc.ServerCert
		err  error
		want string
	}{
		{"fine", irc.ServerCert{Trusted: true, NotAfter: time.Now().Add(100 * time.Hour)}, nil, ""},
		{"unreachable", irc.ServerCert{}, errors.New("i/o timeout"), ""},
		{"expired", irc.ServerCert{Why: errors.New("x509: certificate has expired")}, nil, "won't trust vps's certificate (x509: certificate has expired)"},
		{"running out", irc.ServerCert{Trusted: true, NotAfter: time.Now().Add(20 * time.Hour)}, nil, "vps's certificate runs out"},
	} {
		got, gotErr = c.cert, c.err
		before := len(warnings())
		a.certWatched = nil // a new session
		run(a, a.watchCert())
		if a.watchCert() != nil {
			t.Fatalf("%s: checked again on reconnecting", c.what)
		}
		w := warnings()
		switch {
		case c.want == "" && len(w) != before:
			t.Errorf("%s: warned %q", c.what, w[len(w)-1])
		case c.want != "" && (len(w) == before || !strings.Contains(w[len(w)-1], c.want)):
			t.Errorf("%s: warnings %q, want %q", c.what, w, c.want)
		}
	}
}

// TestPhoneCertificate: Connect a Phone says Goguma will trust a bouncer
// with a Let's Encrypt certificate; for a self-signed one it shows the
// SHA-1 to compare and, on a bouncer trout set up, where to get one.
func TestPhoneCertificate(t *testing.T) {
	a, _ := installApp(t, &fakeServer{})
	a.Update(tea.WindowSizeMsg{Width: 110, Height: 30})
	a.bnc = config.Bouncer{Name: "vps", Address: "ircs://203.0.113.9:6697", User: "max", Pin: "SHA256:x", SSH: "vps"}
	cert := irc.ServerCert{SHA1: "0123456789abcdef0123456789abcdef01234567", Why: errors.New("x509: unknown authority")}
	a.hooks.readCert = func(_ context.Context, cfg irc.Config) (irc.ServerCert, error) {
		if cfg.Pin != "SHA256:x" || cfg.Addr != "203.0.113.9:6697" {
			t.Errorf("read the certificate with %+v", cfg)
		}
		return cert, nil
	}
	open := func() string {
		a.dlg = nil
		run(a, a.openPhone())
		text := strings.ReplaceAll(dialogText(a, a.dlg.(*phoneBox)), "▀", "") // the code
		return strings.Join(strings.Fields(text), " ")
	}
	text := open()
	for _, want := range []string{"Accept Always if its SHA1 is:", cert.SHA1, "Tools > Bouncers > Server > Certificate"} {
		if !strings.Contains(text, want) {
			t.Errorf("self-signed: no %q in\n%s", want, text)
		}
	}
	cert = irc.ServerCert{SHA1: cert.SHA1, Issuer: "Let's Encrypt", Trusted: true}
	a.bnc.Cert = letsEncrypt
	text = open()
	if !strings.Contains(text, "Goguma trusts the bouncer's certificate (Let's Encrypt vouches for it).") || strings.Contains(text, "SHA1") {
		t.Errorf("trusted:\n%s", text)
	}
}
