package install

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MaxRenaud/trout/internal/irc"
)

// fakeServer answers each command line with a canned reply, by the first
// pattern it contains, and records what ran.
type fakeServer struct {
	replies []reply
	ran     []Cmd
	stdin   []string
}

type reply struct {
	match  string
	stdout string
	err    error
}

func (s *fakeServer) Run(ctx context.Context, c Cmd) error {
	s.ran = append(s.ran, c)
	in := ""
	if c.Stdin != nil {
		b, _ := io.ReadAll(c.Stdin)
		in = string(b)
	}
	s.stdin = append(s.stdin, in)
	for _, r := range s.replies {
		if strings.Contains(c.Line, r.match) {
			if c.Stdout != nil {
				io.WriteString(c.Stdout, r.stdout)
			}
			return r.err
		}
	}
	return nil
}

func TestProbe(t *testing.T) {
	srv := &fakeServer{replies: []reply{{match: "uname", stdout: "uname=Linux x86_64\nuid=1000\nuser=max\nhome=/home/max\n" +
		"systemd=yes\nos=Debian GNU/Linux 13 (trixie)\nsudo=password\ngroups=max sudo\n"}}}
	f, err := Probe(context.Background(), srv)
	if err != nil {
		t.Fatal(err)
	}
	want := Facts{Arch: "amd64", Machine: "Linux x86_64", OS: "Debian GNU/Linux 13 (trixie)", User: "max",
		Home: "/home/max", Systemd: true, Sudo: SudoPassword, SudoGroup: true}
	if f != want {
		t.Fatalf("got %+v\nwant %+v", f, want)
	}
	if f.BestRoot() != WithPassword || f.Unsupported() != "" {
		t.Fatal(f.BestRoot(), f.Unsupported())
	}

	for _, c := range []struct {
		facts Facts
		root  Root
		why   string
	}{
		{Facts{Arch: "arm64", Systemd: true, Root: true}, AsRoot, ""},
		{Facts{Arch: "amd64", Systemd: true, Sudo: SudoNoPasswd}, WithSudo, ""},
		{Facts{Arch: "amd64", Systemd: true}, ByHand, ""},
		{Facts{Arch: "amd64"}, ByHand, "doesn't run systemd"},
		{Facts{Machine: "FreeBSD amd64", Systemd: true}, ByHand, "FreeBSD amd64"},
	} {
		if c.facts.BestRoot() != c.root || !strings.Contains(c.facts.Unsupported(), c.why) || (c.why == "") != (c.facts.Unsupported() == "") {
			t.Errorf("%+v: root %v, unsupported %q", c.facts, c.facts.BestRoot(), c.facts.Unsupported())
		}
	}
}

func testInstaller(t *testing.T, srv *fakeServer) *Installer {
	t.Helper()
	in, err := New(srv, Facts{Arch: "amd64", Home: "/home/max", Systemd: true, Sudo: SudoNoPasswd}, "192.0.2.7", "max")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for _, f := range []string{"soju", "sojuctl"} {
		os.WriteFile(filepath.Join(dir, f), []byte(f+" binary"), 0o755)
	}
	in.Binaries = func(arch string) (string, string, error) {
		return filepath.Join(dir, "soju"), filepath.Join(dir, "sojuctl"), nil
	}
	return in
}

func TestNewAndScript(t *testing.T) {
	if _, err := New(nil, Facts{}, "host", "max; rm -rf /"); err == nil {
		t.Fatal("an account name with shell in it")
	}
	if _, err := New(nil, Facts{}, "host$(id)", "max"); err == nil {
		t.Fatal("a host with shell in it")
	}
	in := testInstaller(t, &fakeServer{})
	if len(in.Password) != 24 || strings.Trim(in.Password, "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789") != "" {
		t.Fatalf("password %q", in.Password)
	}
	// The pin is the uploaded certificate's key, and the certificate names the IP.
	block, _ := pem.Decode(in.cert)
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil || irc.Fingerprint(cert) != in.Pin || len(cert.IPAddresses) != 1 || cert.IPAddresses[0].String() != "192.0.2.7" {
		t.Fatalf("certificate: %v %v", cert.IPAddresses, err)
	}
	s := in.Script()
	for _, want := range []string{"#!/bin/sh\n", "TROUT_HOST=192.0.2.7\n", "TROUT_INGRESS=https://192.0.2.7:8443\n",
		"TROUT_ACCOUNT=max\n", "TROUT_PASSWORD=" + in.Password + "\n", "TROUT_SOJU_VERSION=" + SojuVersion + "\n", "set -eu"} {
		if !strings.Contains(s, want) {
			t.Errorf("the script has no %q", want)
		}
	}
	if in.Address() != "ircs://192.0.2.7:6697" || in.ManualCommand() != "sudo sh /home/max/.trout-install/setup.sh" {
		t.Fatal(in.Address(), in.ManualCommand())
	}
	in.Host = "2001:db8::1"
	if !strings.Contains(in.Script(), "TROUT_INGRESS=https://[2001:db8::1]:8443\n") || in.Address() != "ircs://[2001:db8::1]:6697" {
		t.Fatal("an IPv6 address isn't bracketed")
	}
}

func TestStageFiles(t *testing.T) {
	srv := &fakeServer{}
	in := testInstaller(t, srv)
	if err := in.StageFiles(context.Background()); err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, c := range srv.ran {
		lines = append(lines, c.Line)
	}
	want := []string{
		"rm -rf /home/max/.trout-install && mkdir -m 700 /home/max/.trout-install",
		"cat > /home/max/.trout-install/soju && chmod 755 /home/max/.trout-install/soju",
		"cat > /home/max/.trout-install/sojuctl && chmod 755 /home/max/.trout-install/sojuctl",
		"cat > /home/max/.trout-install/tls.crt && chmod 600 /home/max/.trout-install/tls.crt",
		"cat > /home/max/.trout-install/tls.key && chmod 600 /home/max/.trout-install/tls.key",
		"cat > /home/max/.trout-install/setup.sh && chmod 600 /home/max/.trout-install/setup.sh",
	}
	if !slices.Equal(lines, want) {
		t.Fatalf("ran:\n%s", strings.Join(lines, "\n"))
	}
	if srv.stdin[1] != "soju binary" || srv.stdin[5] != in.Script() || !strings.Contains(srv.stdin[4], "EC PRIVATE KEY") {
		t.Fatal("uploaded the wrong things")
	}
}

func TestSetupTiers(t *testing.T) {
	for _, c := range []struct {
		root  Root
		line  string
		stdin string
	}{
		{AsRoot, "sh /home/max/.trout-install/setup.sh ", ""},
		{WithSudo, "sudo -n sh /home/max/.trout-install/setup.sh ", ""},
		{WithPassword, "sudo -k -S -p '' sh /home/max/.trout-install/setup.sh ", "hunter2\n"},
	} {
		srv := &fakeServer{replies: []reply{{match: "setup.sh", stdout: "trout-step: Starting soju\ntrout-step: done\n"}}}
		in := testInstaller(t, srv)
		in.Root, in.SudoPassword = c.root, "hunter2"
		var steps []string
		in.Progress = func(s string) { steps = append(steps, s) }
		if err := in.Setup(context.Background()); err != nil {
			t.Fatal(err)
		}
		if srv.ran[0].Line != c.line || srv.stdin[0] != c.stdin || !slices.Equal(steps, []string{"Starting soju"}) {
			t.Errorf("%v: ran %q with %q, steps %q", c.root, srv.ran[0].Line, srv.stdin[0], steps)
		}
	}
	in := testInstaller(t, &fakeServer{})
	in.Root = ByHand
	if err := in.Setup(context.Background()); err == nil || !strings.Contains(err.Error(), in.ManualCommand()) {
		t.Fatalf("ByHand: %v", err)
	}
}

func TestSetupErrors(t *testing.T) {
	sudoSays := func(msg string) *fakeServer {
		return &fakeServer{replies: []reply{{match: "setup.sh", err: &RemoteError{Code: 1, Stderr: msg}}}}
	}
	for _, c := range []struct {
		srv  *fakeServer
		want error
	}{
		{sudoSays("sudo: 1 incorrect password attempt"), ErrSudoPassword},
		{sudoSays("Sorry, try again.\nsudo: no password was provided"), ErrSudoPassword},
		{sudoSays("sudo: Authentication failed, try again."), ErrSudoPassword}, // sudo-rs
		{sudoSays("plain is not in the sudoers file."), ErrNoSudo},
		{sudoSays("Sorry, user plain is not allowed to execute '/bin/sh' as root"), ErrNoSudo},
		{sudoSays("sudo: I'm sorry plain. I'm afraid I can't do that"), ErrNoSudo}, // sudo-rs
	} {
		in := testInstaller(t, c.srv)
		in.Root, in.SudoPassword = WithPassword, "x"
		if err := in.Setup(context.Background()); !errors.Is(err, c.want) {
			t.Errorf("%q: got %v, want %v", c.srv.replies[0].err, err, c.want)
		}
	}

	// The script's own failure line wins over the exit status.
	srv := &fakeServer{replies: []reply{{match: "setup.sh", stdout: "trout-step: Starting soju\ntrout-fail: soju didn't start: bind: address in use\n",
		err: &RemoteError{Code: 1}}}}
	in := testInstaller(t, srv)
	if err := in.Setup(context.Background()); err == nil || err.Error() != "soju didn't start: bind: address in use" {
		t.Fatalf("got %v", err)
	}

	// requiretty: the same again through a terminal, with the password again.
	srv = &fakeServer{replies: []reply{{match: "setup.sh", err: &RemoteError{Code: 1, Stderr: "sudo: sorry, you must have a tty to run sudo"}}}}
	in = testInstaller(t, srv)
	in.Root, in.SudoPassword = WithPassword, "pw"
	in.Setup(context.Background())
	if len(srv.ran) != 2 || srv.ran[0].TTY || !srv.ran[1].TTY || srv.stdin[1] != "pw\n" {
		t.Fatalf("requiretty: ran %+v", srv.ran)
	}
}

func TestSSHErrors(t *testing.T) {
	for _, c := range []struct {
		stderr string
		kind   SSHFailure
		you    bool
	}{
		{"Host key verification failed.", SSHHostKey, true},
		{"@@@ WARNING: REMOTE HOST IDENTIFICATION HAS CHANGED! @@@", SSHHostKey, true},
		{"max@vps: Permission denied (publickey).", SSHAuth, true},
		{"ssh: Could not resolve hostname nope: nodename nor servname provided", SSHUnreachable, false},
		{"ssh: connect to host 192.0.2.1 port 22: Operation timed out", SSHUnreachable, false},
		{"ssh: connect to host 10.0.0.1 port 22: Connection refused", SSHUnreachable, false},
		{"kex_exchange_identification: read: Connection reset by peer", SSHOther, false},
	} {
		e := sshError(c.stderr)
		if e.Kind != c.kind || e.NeedsYou() != c.you {
			t.Errorf("%q: kind %v, needs you %v", c.stderr, e.Kind, e.NeedsYou())
		}
	}
}

func TestSSHArgs(t *testing.T) {
	s := SSH{Host: "vps", Options: []string{"-F", "test_config"}}
	got := s.args("uname -sm", false, true)
	want := []string{"-o", "ConnectTimeout=15", "-o", "ServerAliveInterval=15", "-o", "BatchMode=yes", "-T",
		"-F", "test_config", "vps", "--", "uname -sm"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %q", got)
	}
	if i := s.Interactive(); slices.Contains(i.Args, "BatchMode=yes") || i.Args[len(i.Args)-1] != "true" {
		t.Fatalf("Interactive: %q", i.Args)
	}
}

func TestFindBinaries(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache) // Linux
	t.Setenv("HOME", cache)           // macOS: ~/Library/Caches
	if _, _, err := FindBinaries("arm64"); err == nil || !strings.Contains(err.Error(), "dev/build-soju") {
		t.Fatalf("with nothing built: %v", err)
	}
	base, _ := os.UserCacheDir()
	dir := filepath.Join(base, "trout", "soju", SojuVersion, "linux-arm64")
	os.MkdirAll(dir, 0o755)
	for _, f := range []string{"soju", "sojuctl"} {
		os.WriteFile(filepath.Join(dir, f), nil, 0o755)
	}
	soju, sojuctl, err := FindBinaries("arm64")
	if err != nil || soju != filepath.Join(dir, "soju") || sojuctl != filepath.Join(dir, "sojuctl") {
		t.Fatal(soju, sojuctl, err)
	}
}

func TestModes(t *testing.T) {
	f := Facts{Arch: "amd64", Home: "/home/max", Systemd: true, Sudo: SudoNoPasswd}
	if _, err := Existing(nil, f, "vps", "max", "pw", "SHA256:x", "reinstall"); err == nil {
		t.Fatal("an unknown mode")
	}
	for _, c := range []struct {
		mode    string
		uploads []string
	}{
		{"update", []string{"soju", "sojuctl", "setup.sh"}}, // no new key: the pin stays
		{"remove", []string{"setup.sh"}},
		{"purge", []string{"setup.sh"}},
	} {
		srv := &fakeServer{}
		in, err := Existing(srv, f, "vps", "max", "pw", "SHA256:x", c.mode)
		if err != nil {
			t.Fatal(err)
		}
		dir := t.TempDir()
		for _, b := range []string{"soju", "sojuctl"} {
			os.WriteFile(filepath.Join(dir, b), nil, 0o755)
		}
		in.Binaries = func(string) (string, string, error) {
			return filepath.Join(dir, "soju"), filepath.Join(dir, "sojuctl"), nil
		}
		if err := in.StageFiles(context.Background()); err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, r := range srv.ran[1:] {
			got = append(got, strings.TrimPrefix(strings.Fields(r.Line)[2], "/home/max/.trout-install/"))
		}
		if !slices.Equal(got, c.uploads) {
			t.Errorf("%s uploads %q", c.mode, got)
		}
		// The script leaves the account alone, and runs in the mode.
		if !strings.Contains(in.Script(), "TROUT_PASSWORD=\n") || in.ManualCommand() != "sudo sh /home/max/.trout-install/setup.sh "+c.mode {
			t.Errorf("%s: %q", c.mode, in.ManualCommand())
		}
		in.Setup(context.Background())
		if last := srv.ran[len(srv.ran)-1].Line; last != "sudo -n sh /home/max/.trout-install/setup.sh "+c.mode {
			t.Errorf("%s ran %q", c.mode, last)
		}
	}
}

// repo is a path from the repository's root.
func repo(p string) string { return filepath.Join("..", "..", p) }
