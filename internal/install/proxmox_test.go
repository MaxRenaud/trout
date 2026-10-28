//go:build proxmox

// The whole install on real servers: throwaway clones of the Proxmox
// templates (dev/proxmox, credentials in tmp/proxmox.env), one per distro,
// through every way of getting root. Needs dev/build-soju first.
//
//	go test -tags proxmox ./internal/install -v -parallel 3
//	TROUT_PVE_TEMPLATES="debian-13 rocky-10" go test -tags proxmox ./internal/install -v
package install

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

var templates = []string{"debian-12", "debian-13", "ubuntu-2404", "ubuntu-2604", "rocky-9", "rocky-10"}

func TestProxmoxInstall(t *testing.T) {
	if _, _, err := FindBinaries("amd64"); err != nil {
		t.Fatal(err)
	}
	list := templates
	if s := os.Getenv("TROUT_PVE_TEMPLATES"); s != "" {
		list = strings.Fields(s)
	}
	for _, tpl := range list {
		t.Run(tpl, func(t *testing.T) {
			t.Parallel()
			installOn(t, tpl)
		})
	}
}

func installOn(t *testing.T, tpl string) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	ip := vmUp(t, tpl)
	tester := sshAs(ip, "tester")
	sh := func(r Runner, line string) string {
		t.Helper()
		out, err := output(ctx, r, line)
		if err != nil {
			t.Fatalf("%s: %v\n%s", line, err, out)
		}
		return out
	}

	f, err := Probe(ctx, tester)
	if err != nil {
		t.Fatal(err)
	}
	if f.Arch != "amd64" || !f.Systemd || f.Root || f.Sudo != SudoNoPasswd || f.Unsupported() != "" {
		t.Fatalf("probe: %+v", f)
	}
	t.Logf("%s: %s", tpl, f.OS)

	// A firewall in the way, as on many real servers: the setup opens it.
	switch {
	case strings.HasPrefix(tpl, "ubuntu"):
		sh(tester, "sudo ufw allow 22/tcp >/dev/null && sudo ufw --force enable >/dev/null")
	case strings.HasPrefix(tpl, "rocky"):
		sh(tester, "sudo dnf -q -y install firewalld >/dev/null && sudo systemctl enable --now firewalld")
	}

	install := func(r Runner, f Facts, root Root, password string) *Installer {
		t.Helper()
		in, err := New(r, f, ip, "max")
		if err != nil {
			t.Fatal(err)
		}
		in.Root, in.SudoPassword = root, password
		in.Progress = func(s string) { t.Logf("%s %s: %s", time.Now().Format("15:04:05"), tpl, s) }
		if err := in.StageFiles(ctx); err != nil {
			t.Fatal(err)
		}
		return in
	}
	check := func(in *Installer, what string) {
		t.Helper()
		if err := in.Check(ctx); err != nil {
			t.Fatalf("%s: %v", what, err)
		}
	}

	t.Logf("%s %s: --- %s", time.Now().Format("15:04:05"), tpl, "Passwordless sudo")
	// Passwordless sudo, then the same again: the setup is safe to re-run.
	in := install(tester, f, WithSudo, "")
	if err := in.Setup(ctx); err != nil {
		t.Fatal(err)
	}
	check(in, "with sudo")
	if err := in.Setup(ctx); err != nil {
		t.Fatal("running it again:", err)
	}
	check(in, "running it again")
	if got := sh(tester, "sudo stat -c '%U:%G %a' /etc/soju /etc/soju/tls.key /etc/soju/config /var/lib/soju"); got != "root:soju 750\nroot:soju 640\nroot:soju 640\nsoju:soju 750\n" {
		t.Fatalf("owners and modes:\n%s", got)
	}
	if err := in.Cleanup(ctx); err != nil {
		t.Fatal(err)
	}

	t.Logf("%s %s: --- %s", time.Now().Format("15:04:05"), tpl, "As root")
	// As root, with the cloud image's "log in as the user" key replaced.
	sh(tester, "sudo cp ~/.ssh/authorized_keys /root/.ssh/authorized_keys")
	root := sshAs(ip, "root")
	rf, err := Probe(ctx, root)
	if err != nil || !rf.Root || rf.BestRoot() != AsRoot {
		t.Fatalf("probe as root: %+v %v", rf, err)
	}
	in = install(root, rf, AsRoot, "")
	if err := in.Setup(ctx); err != nil {
		t.Fatal("as root:", err)
	}
	check(in, "as root")

	t.Logf("%s %s: --- %s", time.Now().Format("15:04:05"), tpl, "sudo with a password")
	// sudo with a password: a wrong one is ErrSudoPassword, then the right one.
	sudoers := "sudo"
	if strings.HasPrefix(tpl, "rocky") {
		sudoers = "wheel"
	}
	addUser(t, sh, tester, "pw", "Secret-123", sudoers)
	pw := sshAs(ip, "pw")
	pf, err := Probe(ctx, pw)
	if err != nil || pf.Sudo != SudoPassword || !pf.SudoGroup || pf.BestRoot() != WithPassword {
		t.Fatalf("probe with a sudo password: %+v %v", pf, err)
	}
	in = install(pw, pf, WithPassword, "wrong")
	if err := in.Setup(ctx); !errors.Is(err, ErrSudoPassword) {
		t.Fatalf("a wrong sudo password: %v", err)
	}
	in.SudoPassword = "Secret-123"
	if err := in.Setup(ctx); err != nil {
		t.Fatal("with a sudo password:", err)
	}
	check(in, "with a sudo password")

	t.Logf("%s %s: --- %s", time.Now().Format("15:04:05"), tpl, "No sudo for this one")
	// No sudo for this one: sudo says so, and the person runs the script.
	addUser(t, sh, tester, "plain", "Plain-123", "")
	plain := sshAs(ip, "plain")
	nf, err := Probe(ctx, plain)
	if err != nil || nf.SudoGroup {
		t.Fatalf("probe without sudo: %+v %v", nf, err)
	}
	in = install(plain, nf, WithPassword, "Plain-123")
	if err := in.Setup(ctx); !errors.Is(err, ErrNoSudo) {
		t.Fatalf("sudo for someone not allowed: %v", err)
	}
	in.Root = ByHand
	if err := in.Check(ctx); err == nil {
		t.Fatal("Check passed before the script ran (the old account still works?)")
	}
	sh(tester, strings.Replace(in.ManualCommand(), "sudo sh", "sudo sh", 1)) // what the person runs
	check(in, "run by hand")

	t.Logf("%s %s: --- %s", time.Now().Format("15:04:05"), tpl, "Update, then purge")
	// Update: trout's soju again, same key and account, so the last
	// install's password and pin still work.
	up, err := Existing(tester, f, ip, "max", in.Password, in.Pin, "update")
	if err != nil {
		t.Fatal(err)
	}
	up.Root, up.Progress = WithSudo, in.Progress
	if err := up.StageFiles(ctx); err != nil {
		t.Fatal(err)
	}
	if err := up.Setup(ctx); err != nil {
		t.Fatal("update:", err)
	}
	check(up, "after an update")

	// Purge: nothing left, and Check says so.
	in, err = Existing(tester, f, ip, "max", in.Password, in.Pin, "purge")
	if err != nil {
		t.Fatal(err)
	}
	in.Root = WithSudo
	if err := in.StageFiles(ctx); err != nil {
		t.Fatal(err)
	}
	if err := in.Setup(ctx); err != nil {
		t.Fatal("purge:", err)
	}
	if got := sh(tester, "systemctl is-active soju || true; ls /etc/soju /var/lib/soju /usr/local/bin/soju 2>&1 | wc -l; id soju 2>&1 | grep -c 'no such' || true"); !strings.HasPrefix(got, "inactive\n") {
		t.Fatalf("after purge:\n%s", got)
	}
	if err := in.Check(ctx); err == nil || !strings.Contains(err.Error(), "isn't finished") {
		t.Fatalf("Check after purge: %v", err)
	}
}

func addUser(t *testing.T, sh func(Runner, string) string, admin Runner, name, password, group string) {
	t.Helper()
	line := "sudo useradd -m -s /bin/bash " + name + " && echo '" + name + ":" + password + "' | sudo chpasswd" +
		" && sudo install -d -m 700 -o " + name + " -g " + name + " /home/" + name + "/.ssh" +
		" && sudo install -m 600 -o " + name + " -g " + name + " ~/.ssh/authorized_keys /home/" + name + "/.ssh/"
	if group != "" {
		line += " && sudo usermod -aG " + group + " " + name
	}
	sh(admin, line)
}

// vmUp clones tpl, boots it and returns its address; the VM is destroyed
// when the test ends.
func vmUp(t *testing.T, tpl string) string {
	t.Helper()
	out, err := exec.Command("python3", "-I", repo("dev/proxmox/pve_vm.py"), "up", tpl).CombinedOutput()
	if err != nil {
		t.Fatalf("pve_vm.py up %s: %v\n%s", tpl, err, out)
	}
	f := strings.Fields(string(out))
	if len(f) != 2 {
		t.Fatalf("pve_vm.py up %s said %q", tpl, out)
	}
	t.Cleanup(func() {
		if os.Getenv("TROUT_PVE_KEEP") != "" {
			t.Logf("keeping VM %s at %s", f[0], f[1])
			return
		}
		if out, err := exec.Command("python3", "-I", repo("dev/proxmox/pve_vm.py"), "down", f[0]).CombinedOutput(); err != nil {
			t.Errorf("pve_vm.py down %s: %v\n%s", f[0], err, out)
		}
	})
	return f[1]
}

func sshAs(ip, user string) SSH {
	return SSH{Host: user + "@" + ip, Options: []string{"-i", repo("tmp/pve_ed25519"),
		"-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null", "-o", "LogLevel=ERROR"}}
}
