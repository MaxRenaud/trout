//go:build cloud

// The setup wizard making a real server at each provider (tokens in
// tmp/cloud.env), installing soju on it, connecting to it with its pinned
// key and moving a network there, then Bouncers > Remove > Delete Server.
// Costs a few cents per provider per run.
//
//	go test -tags cloud ./internal/ui -run TestCloudBouncer -v
package ui

import (
	"bufio"
	"context"
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
	for _, arch := range []string{"amd64", "arm64"} { // before installApp moves HOME
		s, c, err := install.FindBinaries(arch)
		if err != nil {
			t.Fatal(err)
		}
		soju[arch] = [2]string{s, c}
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
				binaries: func(arch string) (string, string, error) { return soju[arch][0], soju[arch][1], nil },
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
