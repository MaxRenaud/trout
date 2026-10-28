//go:build proxmox

// The setup wizard for real: ssh to a throwaway Proxmox clone (see
// internal/install's proxmox test), install, connect to the new bouncer
// with its pinned key, and move a network onto it.
//
//	go test -tags proxmox ./internal/ui -run TestProxmoxWizard -v
package ui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MaxRenaud/trout/internal/install"
)

func TestProxmoxWizard(t *testing.T) {
	repo := func(p string) string { return filepath.Join("..", "..", p) }
	out, err := exec.Command("python3", "-I", repo("dev/proxmox/pve_vm.py"), "up", "debian-13").CombinedOutput()
	if err != nil {
		t.Fatalf("pve_vm.py up: %v\n%s", err, out)
	}
	vm := strings.Fields(string(out))
	t.Cleanup(func() {
		if os.Getenv("TROUT_PVE_KEEP") == "" {
			exec.Command("python3", "-I", repo("dev/proxmox/pve_vm.py"), "down", vm[0]).Run()
		}
	})
	ssh := install.SSH{Host: "tester@" + vm[1], Options: []string{"-i", repo("tmp/pve_ed25519"),
		"-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null", "-o", "LogLevel=ERROR"}}

	soju, sojuctl, err := install.FindBinaries("amd64") // before installApp moves HOME
	if err != nil {
		t.Fatal(err)
	}
	a, _ := installApp(t, &fakeServer{})
	a.hooks = &installHooks{ // real ssh, soju and login
		remote:   func(string) remote { return ssh },
		binaries: func(string) (string, string, error) { return soju, sojuctl, nil },
	}
	// The network to move: an address that goes nowhere, so the bouncer
	// doesn't really join Libera from the test VM.
	a.nets[1].Server = "irc.example.invalid"

	openOwnServer(a)
	w := wizard(t, a)
	w.typed, w.choice = ssh.Host, len(w.hosts)
	_, cmd := a.Update(tea.KeyMsg{Type: tea.KeyEnter}) // Install
	drive(a, cmd)
	if w.step != instDone {
		t.Fatalf("step %d, err %q\n%s", w.step, w.err, strings.Join(w.log, "\n"))
	}
	t.Logf("log:\n%s", strings.Join(w.log, "\n"))

	_, cmd = a.Update(tea.KeyMsg{Type: tea.KeyEnter}) // Finish: switch to the new bouncer
	if a.bnc.Address != "ircs://"+vm[1]+":6697" || a.bnc.Pin == "" || len(a.moving) != 1 {
		t.Fatalf("bouncer %+v, moving %v", a.bnc, a.moving)
	}
	up := runUntil[ircUpMsg](t, cmd)
	a.Update(up) // the control connection: pinned key, SASL, then the move

	// The new bouncer made the network and its channel.
	deadline := time.Now().Add(20 * time.Second)
	for {
		got, _ := exec.Command("ssh", append(ssh.Options, ssh.Host, "--",
			"sudo /usr/local/bin/sojuctl -config /etc/soju/config user run max channel status -network Libera")...).CombinedOutput()
		if strings.Contains(string(got), "#go") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no #go on the new bouncer's Libera:\n%s", got)
		}
		time.Sleep(time.Second)
	}
}
