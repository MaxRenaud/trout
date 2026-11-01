package main

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/letsencrypt/pebble/v2/ca"
	"github.com/letsencrypt/pebble/v2/db"
	"github.com/letsencrypt/pebble/v2/va"
	"github.com/letsencrypt/pebble/v2/wfe"
)

// pebble is Let's Encrypt's test CA, run here: it checks http-01 by
// fetching from 127.0.0.1 on vaPort, like Let's Encrypt on port 80, and
// has a "shortlived" profile like Let's Encrypt's.
type pebble struct {
	dir    string       // the directory URL
	client *http.Client // trusts its HTTPS
	roots  *x509.CertPool
	vaPort int
}

func startPebble(t *testing.T) *pebble {
	t.Helper()
	t.Setenv("PEBBLE_VA_NOSLEEP", "1")
	t.Setenv("PEBBLE_WFE_NONCEREJECT", "0")
	t.Setenv("PEBBLE_AUTHZREUSE", "0") // every order is checked again
	logger := log.New(io.Discard, "", 0)
	store := db.NewMemoryStore()
	authority := ca.New(logger, store, "", "ecdsa", 0, 1, map[string]ca.Profile{
		"default":  {Description: "90 days"},
		shortlived: {Description: "160 hours, for IP addresses", ValidityPeriod: 160 * 3600},
	})
	port := freePort(t)
	v := va.New(logger, port, freePort(t), false, "", store)
	front := wfe.New(logger, store, v, authority, []string{"pebble.letsencrypt.org"}, false, false, 3, 5)
	srv := httptest.NewTLSServer(front.Handler())
	t.Cleanup(srv.Close)
	roots := x509.NewCertPool()
	roots.AddCert(authority.GetRootCert(0).Cert)
	return &pebble{dir: srv.URL + wfe.DirectoryPath, client: srv.Client(), roots: roots, vaPort: port}
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// sojuDir is /etc/soju as trout's setup leaves it: soju's key and the
// self-signed certificate for it.
func sojuDir(t *testing.T, host string) (dir string, key *ecdsa.PrivateKey) {
	t.Helper()
	dir = t.TempDir()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, _ := x509.MarshalECPrivateKey(key)
	os.WriteFile(filepath.Join(dir, "tls.key"), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}), 0o640)
	os.WriteFile(filepath.Join(dir, "tls.crt"), nil, 0o640)
	r := &renewer{Host: host, Dir: dir, Out: io.Discard}
	if err := r.SelfSigned(); err != nil {
		t.Fatal(err)
	}
	return dir, key
}

func (p *pebble) renewer(t *testing.T, dir string, out io.Writer) *renewer {
	return &renewer{Host: "127.0.0.1", Dir: dir, State: t.TempDir(), ACME: p.dir, Client: p.client,
		HTTP: "127.0.0.1:" + strconv.Itoa(p.vaPort), Out: out}
}

// chain reads tls.crt and checks it the way a phone would: signed by
// the CA, for the host, for soju's key.
func (p *pebble) chain(t *testing.T, dir string, key *ecdsa.PrivateKey) *x509.Certificate {
	t.Helper()
	data, _ := os.ReadFile(filepath.Join(dir, "tls.crt"))
	var certs []*x509.Certificate
	for block, rest := pem.Decode(data); block != nil; block, rest = pem.Decode(rest) {
		c, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			t.Fatal(err)
		}
		certs = append(certs, c)
	}
	if len(certs) < 2 {
		t.Fatalf("tls.crt has %d certificates, want the leaf and its intermediate", len(certs))
	}
	inter := x509.NewCertPool()
	for _, c := range certs[1:] {
		inter.AddCert(c)
	}
	if _, err := certs[0].Verify(x509.VerifyOptions{Roots: p.roots, Intermediates: inter, DNSName: "127.0.0.1"}); err != nil {
		t.Fatalf("a phone wouldn't trust it: %v", err)
	}
	if !key.PublicKey.Equal(certs[0].PublicKey) {
		t.Fatal("the certificate isn't for soju's key: trout's pin would break")
	}
	return certs[0]
}

// TestRenew: the first run swaps trout's self-signed certificate for the
// CA's (short-lived for an IP address), keeping soju's key; later runs
// leave it until half its time is up; self-signed puts trout's back.
func TestRenew(t *testing.T) {
	p := startPebble(t)
	dir, key := sojuDir(t, "127.0.0.1")
	trouts, _ := os.ReadFile(filepath.Join(dir, "tls.crt"))
	var out bytes.Buffer
	r := p.renewer(t, dir, &out)
	r.Steps = true
	if err := r.Renew(t.Context()); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	leaf := p.chain(t, dir, key)
	if life := leaf.NotAfter.Sub(leaf.NotBefore); life > 161*time.Hour || life < 159*time.Hour {
		t.Errorf("lasts %s, not the shortlived profile's 160 hours", life)
	}
	for _, want := range []string{"trout-step: Getting 127.0.0.1 a certificate from 127.0.0.1:", "it has the self-signed one trout made",
		"is checking this server over port 80...", "trout-step: Got it: Pebble"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("no %q in:\n%s", want, out.String())
		}
	}
	if saved, _ := os.ReadFile(filepath.Join(dir, "tls.crt.self-signed")); !bytes.Equal(saved, trouts) {
		t.Error("trout's self-signed certificate wasn't kept")
	}
	if st, err := os.Stat(filepath.Join(r.State, "account.key")); err != nil || st.Mode().Perm() != 0o600 {
		t.Errorf("account key: %v %v", st, err)
	}
	if st, _ := os.Stat(filepath.Join(dir, "tls.crt")); st.Mode().Perm() != 0o640 {
		t.Errorf("tls.crt is %v; soju reads it through its group", st.Mode())
	}

	// Not due: nothing changes.
	out.Reset()
	before, _ := os.ReadFile(filepath.Join(dir, "tls.crt"))
	if err := r.Renew(t.Context()); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(dir, "tls.crt"))
	if !bytes.Equal(before, after) || !strings.Contains(out.String(), "is good until") {
		t.Fatalf("renewed a fresh certificate:\n%s", out.String())
	}

	// Half its time up: renewed, same key. The CA backdates, so a bit past half.
	r.Now = func() time.Time { return leaf.NotBefore.Add(leaf.NotAfter.Sub(leaf.NotBefore)/2 + time.Minute) }
	out.Reset()
	if err := r.Renew(t.Context()); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if again := p.chain(t, dir, key); again.SerialNumber.Cmp(leaf.SerialNumber) == 0 || !strings.Contains(out.String(), "half its time is up") {
		t.Fatalf("not renewed:\n%s", out.String())
	}
	if saved, _ := os.ReadFile(filepath.Join(dir, "tls.crt.self-signed")); !bytes.Equal(saved, trouts) {
		t.Error("renewing replaced the kept self-signed certificate")
	}

	// Back to trout's own.
	if err := r.SelfSigned(); err != nil {
		t.Fatal(err)
	}
	if now, _ := os.ReadFile(filepath.Join(dir, "tls.crt")); !bytes.Equal(now, trouts) {
		t.Fatal("self-signed didn't put trout's certificate back")
	}
	if _, err := os.Stat(filepath.Join(dir, "tls.crt.self-signed")); err == nil {
		t.Error("the kept copy is still there")
	}
}

// TestRenewFails: what goes wrong says what to do, and leaves the
// certificate as it was.
func TestRenewFails(t *testing.T) {
	p := startPebble(t)
	dir, _ := sojuDir(t, "127.0.0.1")
	trouts, _ := os.ReadFile(filepath.Join(dir, "tls.crt"))
	unchanged := func() {
		t.Helper()
		if now, _ := os.ReadFile(filepath.Join(dir, "tls.crt")); !bytes.Equal(now, trouts) {
			t.Fatal("a failed renewal changed tls.crt")
		}
	}

	// Port 80 closed: the CA's fetch is refused.
	r := p.renewer(t, dir, io.Discard)
	r.HTTP = "127.0.0.1:" + strconv.Itoa(freePort(t)) // not where the CA looks
	err := r.Renew(t.Context())
	if err == nil || !strings.Contains(err.Error(), "Allow TCP port 80 from anywhere") {
		t.Fatalf("port 80 closed: %v", err)
	}
	unchanged()

	// Something else has port 80.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	r = p.renewer(t, dir, io.Discard)
	r.HTTP = ln.Addr().String()
	if err := r.Renew(t.Context()); err == nil || !strings.Contains(err.Error(), "port 80") {
		t.Fatalf("port 80 taken: %v", err)
	}
	unchanged()

	// No CA there.
	r = p.renewer(t, dir, io.Discard)
	r.ACME = "https://127.0.0.1:1/directory"
	if err := r.Renew(t.Context()); err == nil || !strings.Contains(err.Error(), "couldn't reach 127.0.0.1:1") {
		t.Fatalf("no CA: %v", err)
	}
	unchanged()
}

// TestSelfSignedWithoutCopy: with no kept copy, self-signed makes a new
// certificate for soju's key, so trout's pin still holds.
func TestSelfSignedWithoutCopy(t *testing.T) {
	dir, key := sojuDir(t, "203.0.113.7")
	os.WriteFile(filepath.Join(dir, "tls.crt"), []byte("junk"), 0o640)
	r := &renewer{Host: "203.0.113.7", Dir: dir, Out: io.Discard}
	if err := r.SelfSigned(); err != nil {
		t.Fatal(err)
	}
	c, err := readCert(filepath.Join(dir, "tls.crt"))
	if err != nil || !selfSigned(c) || !key.PublicKey.Equal(c.PublicKey) || c.VerifyHostname("203.0.113.7") != nil {
		t.Fatalf("%v %v", c, err)
	}
	if why := r.due(c, key); !strings.Contains(why, "self-signed") {
		t.Errorf("a self-signed certificate isn't due: %q", why)
	}
}
