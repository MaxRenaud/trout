//go:build cloud

// The setup wizard making a real server at each provider (tokens in
// tmp/cloud.env), installing soju on it, connecting to it with its pinned
// key and moving a network there, getting it a Let's Encrypt certificate
// (the real one, or TROUT_ACME_DIRECTORY's) and taking it away, then
// Bouncers > Remove > Delete Server. Costs a few cents per provider per
// run, and two certificates for the server's throwaway IP address.
//
//	go test -tags cloud ./internal/ui -run TestCloudBouncer -v
package ui

import (
	"bufio"
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MaxRenaud/trout/internal/config"
	"github.com/MaxRenaud/trout/internal/install"
	"github.com/MaxRenaud/trout/internal/irc"
)

func TestCloudBouncer(t *testing.T) {
	repo := func(p string) string { abs, _ := filepath.Abs(filepath.Join("..", "..", p)); return abs }
	env := map[string]string{}
	f, err := os.Open(repo("tmp/cloud.env"))
	if err != nil {
		t.Skip(err)
	}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if k, v, ok := strings.Cut(sc.Text(), "="); ok {
			env[k] = v
		}
	}
	f.Close()
	pub, err := os.ReadFile(repo("tmp/pve_ed25519.pub"))
	if err != nil {
		t.Fatal(err)
	}
	if home, err := os.UserHomeDir(); err == nil { // gcloud's login, before installApp moves HOME
		t.Setenv("CLOUDSDK_CONFIG", filepath.Join(home, ".config", "gcloud"))
	}
	soju := map[string][2]string{}
	certs := map[string]string{}
	for _, arch := range []string{"amd64", "arm64"} { // before installApp moves HOME
		s, c, err := install.FindBinaries(arch)
		if err != nil {
			t.Fatal(err)
		}
		soju[arch] = [2]string{s, c}
		if certs[arch], err = install.FindCert(arch); err != nil {
			t.Fatal(err)
		}
	}
	for i, id := range []string{"hetzner", "digitalocean", "gcloud"} {
		if only := os.Getenv("TROUT_CLOUD"); only != "" && only != id {
			continue
		}
		tok := env[map[string]string{"hetzner": "HCLOUD_TOKEN", "digitalocean": "DO_TOKEN", "gcloud": "GCP_PROJECT"}[id]]
		t.Run(id, func(t *testing.T) { // not parallel: installApp sets HOME
			a, _ := installApp(t, &fakeServer{})
			a.hooks = &installHooks{
				sshKey: strings.TrimSpace(string(pub)),
				remote: func(host string) remote {
					return install.SSH{Host: host, Options: []string{"-i", repo("tmp/pve_ed25519"),
						"-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null", "-o", "LogLevel=ERROR"}}
				},
				binaries:   func(arch string) (string, string, error) { return soju[arch][0], soju[arch][1], nil },
				certBinary: func(arch string) (string, error) { return certs[arch], nil },
			}
			a.saveCloudToken(id, tok)
			a.nets[1].Server = "irc.example.invalid" // moved, but going nowhere

			a.openInstall()
			w := wizard(t, a)
			w.where = i + 1
			_, cmd := a.Update(tea.KeyMsg{Type: tea.KeyEnter}) // Next: regions
			drive(a, cmd)
			if w.step != instRegion {
				t.Fatalf("regions: step %d, %q", w.step, w.err)
			}
			_, cmd = a.Update(tea.KeyMsg{Type: tea.KeyEnter}) // Create Server, the cheapest
			start := time.Now()
			drive(a, cmd)
			if w.server != nil {
				s := *w.server
				t.Cleanup(func() { // whatever happened, the server goes
					// Not t.Context(): it's canceled by the time cleanups run.
					ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
					defer cancel()
					if err := a.provider(id, tok).Delete(ctx, s.ID); err != nil {
						t.Errorf("deleting server %s: %v", s.ID, err)
					}
				})
			}
			t.Logf("%s log:\n%s", id, strings.Join(w.log, "\n"))
			if w.step != instDone {
				t.Fatalf("step %d, err %q", w.step, w.err)
			}
			t.Logf("%s: ready in %s", id, time.Since(start).Round(time.Second))

			_, cmd = a.Update(tea.KeyMsg{Type: tea.KeyEnter}) // Finish: use it
			b := *a.cfg.Find(w.serverName)
			if b.Cloud != id || b.ServerID == "" || a.bnc.Name != b.Name {
				t.Fatalf("saved %+v", b)
			}
			// Play Bubble Tea from here: the control connection's lines come
			// through a.send, and the move waits for the end of its network list.
			msgs := make(chan tea.Msg, 256)
			a.send = func(m tea.Msg) { msgs <- m }
			a.Update(runUntil[ircUpMsg](t, cmd)) // the control connection, pinned
			ssh := a.hooks.remote(b.SSH).(install.SSH)
			deadline := time.Now().Add(45 * time.Second)
			for moved := false; !moved; {
				select {
				case m := <-msgs:
					if _, next := a.Update(m); next != nil {
						go func() { msgs <- next() }()
					}
				case <-time.After(2 * time.Second):
					out, _ := exec.Command("ssh", append(ssh.Options, ssh.Host, "--",
						"sudo -n /usr/local/bin/sojuctl -config /etc/soju/config user run max channel status -network Libera")...).CombinedOutput()
					moved = strings.Contains(string(out), "#go")
					if !moved && time.Now().After(deadline) {
						t.Fatalf("no #go on the new bouncer:\n%s\n%s", out, strings.Join(lineTexts(a.home.Status()), "\n"))
					}
				}
			}

			// Finish asked about a certificate: Get Certificate, from Let's Encrypt.
			testCertificate(t, a, b, ssh)

			// Bouncers > Remove > Delete Server.
			a.openSavedBouncers()
			d := a.dlg.(*savedBouncersBox)
			d.sel = slices.IndexFunc(a.savedBouncers(), func(o config.Bouncer) bool { return o.Name == b.Name })
			d.press(a, "Remove")
			box := a.dlg.(*msgBox)
			a.Update(box.close(a, 0)())
			if a.cfg.Find(b.Name) != nil {
				t.Fatalf("not deleted: %q", lineTexts(a.home.Status()))
			}
		})
	}
}

// testCertificate gets bouncer b a Let's Encrypt certificate from the
// question Finish asked, checks a phone would trust it, that the timer's
// sandboxed service runs and a renewal reloads soju, then goes back to
// self-signed.
func testCertificate(t *testing.T, a *App, b config.Bouncer, ssh install.SSH) {
	t.Helper()
	onServer := func(cmd string) string {
		t.Helper()
		out, err := exec.Command("ssh", append(ssh.Options, ssh.Host, "--", cmd)...).CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v\n%s", cmd, err, out)
		}
		return string(out)
	}
	addr, _, _ := irc.ParseAddress(b.Address)
	host, _, _ := net.SplitHostPort(addr)
	read := func(pin string) irc.ServerCert {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		sc, err := irc.ReadCert(ctx, irc.Config{Addr: addr, Pin: pin})
		if err != nil {
			t.Fatal(err)
		}
		return sc
	}

	start := time.Now()
	drive(a, certBox(t, a).close(a, 0)) // Get Certificate
	w := wizard(t, a)
	t.Logf("certificate log:\n%s", strings.Join(w.log, "\n"))
	if w.mode != "cert" || w.step != instDone {
		t.Fatalf("certificate: step %d, err %q", w.step, w.err)
	}
	t.Logf("certificate in %s", time.Since(start).Round(time.Second))
	a.Update(tea.KeyMsg{Type: tea.KeyEnter}) // Finish
	if a.cfg.Find(b.Name).Cert != letsEncrypt {
		t.Fatal("not saved")
	}
	first := read("") // no pin: the system's authorities alone, like a phone
	if !first.Trusted {
		t.Fatalf("not trusted: %v", first.Why)
	}
	t.Logf("%s vouches for %s until %s", first.Issuer, host, first.NotAfter)

	// The timer's service in its sandbox: not due yet.
	t.Log(onServer("systemctl is-enabled trout-cert.timer && sudo -n systemctl start trout-cert.service && " +
		"sudo -n journalctl -u trout-cert.service -n 2 --no-pager -o cat"))
	// A renewal in the same sandbox, forced, reloads soju without a restart.
	pid := onServer("systemctl show -p MainPID --value soju")
	t.Log(onServer("sudo -n systemd-run --wait --collect --quiet -p ProtectSystem=strict " +
		"-p 'ReadWritePaths=/etc/soju /var/lib/trout-cert' -p NoNewPrivileges=yes -p PrivateTmp=yes -p ProtectHome=yes " +
		"/usr/local/bin/trout-cert renew -force -host " + host + " -acme " + install.ACMEDirectory + " && " +
		"sudo -n journalctl -n 4 --no-pager -o cat _COMM=trout-cert"))
	if again := read(b.Pin); !again.Trusted || again.SHA1 == first.SHA1 {
		t.Fatalf("renewed: trusted %v, same certificate %v", again.Trusted, again.SHA1 == first.SHA1)
	}
	if now := onServer("systemctl show -p MainPID --value soju"); now != pid {
		t.Errorf("soju restarted (%s, was %s): it should only reload", now, pid)
	}

	// Self-Signed: back to the certificate only trout knows.
	a.openServer(*a.cfg.Find(b.Name), nil)
	drive(a, a.dlg.(*msgBox).close(a, 1)) // Certificate
	drive(a, certBox(t, a).close(a, 1))   // Self-Signed
	if w := wizard(t, a); w.step != instDone {
		t.Fatalf("self-signed: step %d, err %q, log %q", w.step, w.err, w.log)
	}
	a.Update(tea.KeyMsg{Type: tea.KeyEnter}) // Finish
	if sc := read(b.Pin); sc.Trusted || a.cfg.Find(b.Name).Cert != "" {
		t.Fatalf("still trusted (%v) or saved (%q)", sc.Trusted, a.cfg.Find(b.Name).Cert)
	}
	if out := onServer("for f in /usr/local/bin/trout-cert /etc/systemd/system/trout-cert.timer /var/lib/trout-cert; do " +
		"if [ -e $f ]; then echo $f is left; fi; done"); out != "" {
		t.Errorf("trout-cert is still there:\n%s", out)
	}
}
