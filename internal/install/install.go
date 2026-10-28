// Package install sets soju up on your own server over SSH: it finds out
// what the server is, gets root (as root, sudo without a password, sudo
// with yours, or a script you run), uploads trout's pinned soju with a
// TLS key made here, runs one setup script that's safe to run again, and
// checks the result end to end by logging in.
package install

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	_ "embed"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/MaxRenaud/trout/internal/irc"
)

// SojuVersion is the soju trout installs; dev/build-soju builds it.
const SojuVersion = "v0.11.1"

//go:embed setup.sh
var setupScript string

// Facts are what Probe found out about the server.
type Facts struct {
	Arch      string // "amd64" or "arm64"; "" for anything else
	Machine   string // uname -sm, for messages
	OS        string // PRETTY_NAME from /etc/os-release
	User      string // who ssh logs in as
	Home      string
	Root      bool // logged in as root
	Systemd   bool
	Sudo      Sudo
	SudoGroup bool // in sudo, wheel or admin: a password probably works
	Installed bool // /usr/local/bin/soju is there (an update, or a re-run)
}

// Sudo is what `sudo -n true` said.
type Sudo int

const (
	SudoNone     Sudo = iota // no sudo command
	SudoNoPasswd             // works without a password
	SudoPassword             // asks for one (or isn't allowed: only trying tells)
)

const probeScript = `echo "uname=$(uname -sm)"; echo "uid=$(id -u)"; echo "user=$(id -un)"; echo "home=$HOME"
if command -v systemctl >/dev/null 2>&1 && [ -d /run/systemd/system ]; then echo systemd=yes; fi
if [ -r /etc/os-release ]; then (. /etc/os-release; echo "os=$PRETTY_NAME"); fi
if command -v sudo >/dev/null 2>&1; then if sudo -n true >/dev/null 2>&1; then echo sudo=nopasswd; else echo sudo=password; fi; fi
echo "groups=$(id -nG)"
if [ -x /usr/local/bin/soju ]; then echo installed=yes; fi`

// Probe finds out what the server is, without changing anything.
func Probe(ctx context.Context, r Runner) (Facts, error) {
	out, err := output(ctx, r, probeScript)
	if err != nil {
		return Facts{}, err
	}
	var f Facts
	for line := range strings.Lines(out) {
		k, v, _ := strings.Cut(strings.TrimRight(line, "\r\n"), "=")
		switch k {
		case "uname":
			f.Machine = v
			switch {
			case !strings.HasPrefix(v, "Linux "):
			case strings.HasSuffix(v, " x86_64"):
				f.Arch = "amd64"
			case strings.HasSuffix(v, " aarch64"), strings.HasSuffix(v, " arm64"):
				f.Arch = "arm64"
			}
		case "uid":
			f.Root = v == "0"
		case "user":
			f.User = v
		case "home":
			f.Home = v
		case "systemd":
			f.Systemd = true
		case "os":
			f.OS = v
		case "sudo":
			f.Sudo = map[string]Sudo{"nopasswd": SudoNoPasswd, "password": SudoPassword}[v]
		case "groups":
			for g := range strings.FieldsSeq(v) {
				f.SudoGroup = f.SudoGroup || g == "sudo" || g == "wheel" || g == "admin"
			}
		case "installed":
			f.Installed = true
		}
	}
	if f.Home == "" {
		return f, errors.New("the server didn't answer the probe: " + strings.TrimSpace(out))
	}
	return f, nil
}

// Unsupported says why trout can't set soju up there, or "".
func (f Facts) Unsupported() string {
	switch {
	case f.Arch == "":
		return "trout's soju runs on Linux on x86_64 or ARM64; this server is " + f.Machine
	case !f.Systemd:
		return "this server doesn't run systemd, which trout's setup needs"
	}
	return ""
}

// Root is how the setup script gets root.
type Root int

const (
	AsRoot       Root = iota // logged in as root
	WithSudo                 // sudo without a password
	WithPassword             // sudo with Installer.SudoPassword
	ByHand                   // you run ManualCommand yourself
)

// BestRoot is the first way to root the facts allow without asking
// anything: as root or passwordless sudo, else WithPassword (if there's a
// sudo at all) or ByHand.
func (f Facts) BestRoot() Root {
	switch {
	case f.Root:
		return AsRoot
	case f.Sudo == SudoNoPasswd:
		return WithSudo
	case f.Sudo == SudoPassword:
		return WithPassword
	}
	return ByHand
}

var (
	// ErrSudoPassword is sudo turning the password down.
	ErrSudoPassword = errors.New("sudo didn't take that password")
	// ErrNoSudo is sudo saying you're not allowed: the script is the way left.
	ErrNoSudo = errors.New("sudo isn't allowed for this user")
)

// Installer sets soju up on one server, or updates or removes what it
// set up. Make it with New (or Existing), then StageFiles, then Setup (or
// have the person run ManualCommand), then Check.
type Installer struct {
	R     Runner
	Facts Facts
	Root  Root
	// Mode is the script's: "" installs, "update" puts in trout's soju and
	// keeps the key and the account's password, "remove" stops and deletes
	// soju but keeps its history, "purge" deletes the history too.
	Mode string
	// SudoPassword is for WithPassword; it's only kept in memory.
	SudoPassword string

	Host     string // the server's address for IRC (from ssh -G)
	Account  string // your soju user name
	Password string // its password, made by New

	// Binaries finds soju and sojuctl for an architecture; FindBinaries
	// if nil.
	Binaries func(arch string) (soju, sojuctl string, err error)
	// Progress, if set, hears each step as it happens.
	Progress func(string)
	// Dial, if set, makes Check's connection (through a proxy, say).
	Dial func(ctx context.Context, network, addr string) (net.Conn, error)

	Pin       string // the TLS key's fingerprint, to pin
	cert, key []byte
}

var accountRe = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)

// New makes the TLS key and the account's password. host is the address
// people (trout) will connect to; account is the soju user name.
func New(r Runner, f Facts, host, account string) (*Installer, error) {
	if !accountRe.MatchString(account) {
		return nil, fmt.Errorf("the account name %q should be letters, digits, '.', '_' or '-'", account)
	}
	if strings.ContainsAny(host, " \t\n'\"$`\\/") || host == "" {
		return nil, fmt.Errorf("can't use %q as the server's address", host)
	}
	in := &Installer{R: r, Facts: f, Root: f.BestRoot(), Host: host, Account: account, Password: password()}
	var err error
	if in.cert, in.key, in.Pin, err = serverCert(host); err != nil {
		return nil, err
	}
	return in, nil
}

// Existing works on a bouncer trout set up before, in mode "update",
// "remove" or "purge": the key and the password on the server stay, and
// pin and password are what Check expects.
func Existing(r Runner, f Facts, host, account, password, pin, mode string) (*Installer, error) {
	if !accountRe.MatchString(account) {
		return nil, fmt.Errorf("the account name %q should be letters, digits, '.', '_' or '-'", account)
	}
	if mode != "update" && mode != "remove" && mode != "purge" {
		return nil, fmt.Errorf("unknown mode %q", mode)
	}
	return &Installer{R: r, Facts: f, Root: f.BestRoot(), Mode: mode, Host: host, Account: account,
		Password: password, Pin: pin}, nil
}

// NewPassword is a new random password for a soju account.
func NewPassword() string { return password() }

// password is 24 letters and digits: plenty, and nothing to quote in sh.
func password() string {
	const chars = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, 24)
	for i := range b {
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(chars))))
		b[i] = chars[n.Int64()]
	}
	return string(b)
}

// serverCert is a self-signed certificate for host with a new key. No
// authority will vouch for it; trout pins the key instead, so it needs no
// domain and never expires in practice.
func serverCert(host string) (certPEM, keyPEM []byte, pin string, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, "", err
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: host},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(30, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	if ip := net.ParseIP(host); ip != nil {
		tmpl.IPAddresses = []net.IP{ip}
	} else {
		tmpl.DNSNames = []string{host}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, "", err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, nil, "", err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, "", err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), irc.Fingerprint(cert), nil
}

func (in *Installer) progress(s string) {
	if in.Progress != nil {
		in.Progress(s)
	}
}

// Stage is the folder on the server the files go to before the script
// installs them; only you can read it.
func (in *Installer) Stage() string { return in.Facts.Home + "/.trout-install" }

// Address is the bouncer's address for config.toml.
func (in *Installer) Address() string { return "ircs://" + net.JoinHostPort(in.Host, "6697") }

// Script is the setup script with this install's settings on top: what
// Stage uploads, and what View Script shows.
func (in *Installer) Script() string {
	ingress := "https://" + net.JoinHostPort(in.Host, "8443")
	password := in.Password
	if in.Mode != "" {
		password = "" // the account stays as it is
	}
	return fmt.Sprintf("#!/bin/sh\nTROUT_SOJU_VERSION=%s\nTROUT_HOST=%s\nTROUT_INGRESS=%s\nTROUT_ACCOUNT=%s\nTROUT_PASSWORD=%s\n\n",
		SojuVersion, in.Host, ingress, in.Account, password) + setupScript
}

// ManualCommand is what you run as root yourself when trout can't (ByHand).
func (in *Installer) ManualCommand() string {
	return strings.TrimSpace("sudo sh " + in.Stage() + "/setup.sh " + in.Mode)
}

// StageFiles uploads the setup script and what it needs: soju and sojuctl
// (not to remove), and the TLS key (a new install).
func (in *Installer) StageFiles(ctx context.Context) error {
	var soju, sojuctl string
	if in.Mode == "" || in.Mode == "update" {
		find := in.Binaries
		if find == nil {
			find = FindBinaries
		}
		var err error
		if soju, sojuctl, err = find(in.Facts.Arch); err != nil {
			return err
		}
	}
	dir := in.Stage()
	if err := in.R.Run(ctx, Cmd{Line: "rm -rf " + dir + " && mkdir -m 700 " + dir}); err != nil {
		return err
	}
	upload := func(name string, data io.Reader, mode string) error {
		return in.R.Run(ctx, Cmd{Line: fmt.Sprintf("cat > %s/%s && chmod %s %s/%s", dir, name, mode, dir, name), Stdin: data})
	}
	for _, b := range []struct{ name, path string }{{"soju", soju}, {"sojuctl", sojuctl}} {
		if b.path == "" {
			continue
		}
		f, err := os.Open(b.path)
		if err != nil {
			return err
		}
		st, _ := f.Stat()
		in.progress(fmt.Sprintf("Uploading %s %s (%d MB)", b.name, SojuVersion, st.Size()>>20))
		err = upload(b.name, f, "755")
		f.Close()
		if err != nil {
			return err
		}
	}
	type file struct {
		name string
		data []byte
	}
	files := []file{{"setup.sh", []byte(in.Script())}}
	if in.cert != nil {
		in.progress("Uploading the TLS key and the setup script")
		files = []file{{"tls.crt", in.cert}, {"tls.key", in.key}, files[0]}
	}
	for _, f := range files {
		if err := upload(f.name, bytes.NewReader(f.data), "600"); err != nil {
			return err
		}
	}
	return nil
}

// Setup runs the setup script as root, the way in.Root says (not ByHand),
// in in.Mode, reporting its steps.
func (in *Installer) Setup(ctx context.Context) error {
	script := "sh " + in.Stage() + "/setup.sh " + in.Mode
	c := Cmd{Line: script}
	switch in.Root {
	case WithSudo:
		c.Line = "sudo -n " + script
	case WithPassword:
		// -k: always read the password, so it never lands in the script's stdin.
		c.Line, c.Stdin = "sudo -k -S -p '' "+script, strings.NewReader(in.SudoPassword+"\n")
	case ByHand:
		return errors.New("this one is yours to run: " + in.ManualCommand())
	}
	err := in.runSteps(ctx, c)
	var re *RemoteError
	if in.Root == WithPassword && errors.As(err, &re) && strings.Contains(re.Stderr, "must have a tty") {
		// An old sudoers with requiretty: the same through a terminal.
		c.TTY, c.Stdin = true, strings.NewReader(in.SudoPassword+"\n")
		err = in.runSteps(ctx, c)
	}
	return err
}

// runSteps runs the script, turning its trout-step lines into progress
// and a trout-fail line into the error.
func (in *Installer) runSteps(ctx context.Context, c Cmd) error {
	pr, pw := io.Pipe()
	c.Stdout = pw
	var failed string
	done := make(chan struct{})
	go func() {
		defer close(done)
		sc := bufio.NewScanner(pr)
		for sc.Scan() {
			line := strings.TrimRight(sc.Text(), "\r")
			if s, ok := strings.CutPrefix(line, "trout-step: "); ok && s != "done" {
				in.progress(s)
			} else if s, ok := strings.CutPrefix(line, "trout-fail: "); ok {
				failed = s
			}
		}
		io.Copy(io.Discard, pr)
	}()
	err := in.R.Run(ctx, c)
	pw.Close()
	<-done
	var re *RemoteError
	switch {
	case failed != "":
		return errors.New(failed)
	case errors.As(err, &re) && in.Root == WithPassword:
		low := strings.ToLower(re.Stderr)
		switch {
		case strings.Contains(low, "not in the sudoers"), strings.Contains(low, "not allowed to"),
			strings.Contains(low, "i'm afraid i can't do that"): // sudo-rs
			return ErrNoSudo
		case strings.Contains(low, "incorrect password"), strings.Contains(low, "no password was provided"),
			strings.Contains(low, "sorry, try again"),
			strings.Contains(low, "authentication failed"): // sudo-rs (Ubuntu 26.04)
			return ErrSudoPassword
		}
	}
	return err
}

// Cleanup removes the uploaded files from the server.
func (in *Installer) Cleanup(ctx context.Context) error {
	return in.R.Run(ctx, Cmd{Line: "rm -rf " + in.Stage()})
}

// Check makes sure soju is set up, without root: it logs in from here
// (pinned key, the account's password). If that fails it looks on the
// server to say what's missing. A nil error means it works.
func (in *Installer) Check(ctx context.Context) error {
	// Its own time limit: a firewall that drops packets makes the dial hang,
	// and there must be time left to look on the server.
	dctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cfg := irc.Config{Addr: net.JoinHostPort(in.Host, "6697"), TLS: true, Pin: in.Pin,
		Nick: in.Account, User: in.Account, Password: in.Password, Dial: in.Dial}
	c, err := irc.Dial(dctx, cfg)
	if err == nil {
		c.Close()
		return nil
	}
	var ce *irc.CertError
	switch {
	case errors.As(err, &ce):
		return fmt.Errorf("something else answers on %s:6697 (key %s, not trout's)", in.Host, ce.Fingerprint)
	case errors.Is(err, irc.ErrLogin):
		return errors.New("soju is up, but your account's password isn't set: run the setup again")
	}
	if missing := in.missing(ctx); missing != "" {
		return errors.New(missing)
	}
	return fmt.Errorf("soju runs on the server, but trout can't reach %s:6697 from here (%v). "+
		"A firewall at your provider is the usual reason: allow TCP 6697 and 8443", in.Host, err)
}

// missing looks on the server for what the setup should have done.
func (in *Installer) missing(ctx context.Context) string {
	out, err := output(ctx, in.R, `id -u soju >/dev/null 2>&1 || echo "no soju system user"
[ -x /usr/local/bin/soju ] || echo "soju isn't in /usr/local/bin"
[ "$(stat -c %U:%G /etc/soju 2>/dev/null)" = root:soju ] || echo "/etc/soju is missing or not root:soju"
[ "$(stat -c %U:%G /var/lib/soju 2>/dev/null)" = soju:soju ] || echo "/var/lib/soju is missing or not soju:soju"
systemctl is-enabled soju >/dev/null 2>&1 || echo "soju.service isn't enabled"
systemctl is-active soju >/dev/null 2>&1 || echo "soju.service isn't running"
ss -ltn 2>/dev/null | grep -q ':6697 ' || echo "nothing listens on port 6697"`)
	if err != nil {
		return "couldn't look on the server: " + err.Error()
	}
	out = strings.TrimSpace(out)
	if out == "" {
		return ""
	}
	return "the setup isn't finished: " + strings.Join(strings.Split(out, "\n"), "; ")
}

// FindBinaries finds soju and sojuctl for linux/arch, to install on a
// server (see FindFor).
func FindBinaries(arch string) (soju, sojuctl string, err error) { return FindFor("linux", arch) }

// FindFor finds soju and sojuctl for goos/arch: next to trout (in
// soju-<goos>-<arch>/, as release archives have them), or where
// dev/build-soju puts them.
func FindFor(goos, arch string) (soju, sojuctl string, err error) {
	platform := goos + "-" + arch
	var dirs []string
	if exe, err := os.Executable(); err == nil {
		dirs = append(dirs, filepath.Join(filepath.Dir(exe), "soju-"+platform))
	}
	if cache, err := os.UserCacheDir(); err == nil {
		dirs = append(dirs, filepath.Join(cache, "trout", "soju", SojuVersion, platform))
	}
	ext := ""
	if goos == "windows" {
		ext = ".exe"
	}
	for _, d := range dirs {
		s, c := filepath.Join(d, "soju"+ext), filepath.Join(d, "sojuctl"+ext)
		if fileExists(s) && fileExists(c) {
			return s, c, nil
		}
	}
	return "", "", fmt.Errorf("no soju %s for %s/%s next to trout; dev/build-soju builds it", SojuVersion, goos, arch)
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}
