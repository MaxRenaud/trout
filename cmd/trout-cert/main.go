// trout-cert gets the bouncer a certificate phones and web browsers trust,
// from Let's Encrypt, and keeps it renewed. trout uploads it with soju
// (setup.sh's cert mode), and a systemd timer runs it twice a day:
//
//	trout-cert renew -host 203.0.113.7         get one if it's due, then reload soju
//	trout-cert self-signed -host 203.0.113.7   go back to a certificate only trout knows
//
// The certificate is for soju's own key (/etc/soju/tls.key), which never
// changes, so trout's pin on that key keeps working through renewals, and
// it still works if a renewal fails and the certificate runs out.
//
// Let's Encrypt checks the server by fetching a token over HTTP on port 80
// (http-01), which trout-cert serves itself for those few seconds. An IP
// address gets the "shortlived" profile, the only one Let's Encrypt has
// for IP addresses: about 6 days, so it's renewed after half of that.
package main

import (
	"bytes"
	"cmp"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/acme"
)

// LetsEncrypt is Let's Encrypt's production directory.
const LetsEncrypt = "https://acme-v02.api.letsencrypt.org/directory"

// shortlived is Let's Encrypt's profile for IP addresses.
const shortlived = "shortlived"

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	cmd := os.Args[1]
	fs := flag.NewFlagSet("trout-cert "+cmd, flag.ExitOnError)
	r := &renewer{Out: os.Stdout}
	fs.StringVar(&r.Host, "host", "", "the bouncer's IP address or name, as trout connects to it")
	fs.StringVar(&r.Dir, "dir", "/etc/soju", "soju's tls.crt and tls.key")
	fs.StringVar(&r.State, "state", "/var/lib/trout-cert", "where the ACME account key is kept")
	fs.StringVar(&r.ACME, "acme", LetsEncrypt, "the ACME directory URL")
	fs.StringVar(&r.HTTP, "http", ":80", "where Let's Encrypt fetches its token (http-01)")
	fs.BoolVar(&r.Force, "force", false, "renew even if the certificate isn't due")
	fs.BoolVar(&r.Steps, "steps", false, "say trout-step: and trout-fail: for trout's setup")
	reload := fs.String("reload", "systemctl try-reload-or-restart soju", "the command that has soju load the new certificate")
	fs.Parse(os.Args[2:])
	r.Reload = strings.Fields(*reload)
	if r.Host == "" {
		usage()
	}
	var err error
	switch cmd {
	case "renew":
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		err = r.Renew(ctx)
		cancel()
	case "self-signed":
		err = r.SelfSigned()
	default:
		usage()
	}
	if err != nil {
		r.fail(err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: trout-cert renew|self-signed -host ADDRESS [flags]")
	os.Exit(2)
}

// renewer is one run of trout-cert on the bouncer's server.
type renewer struct {
	Host   string   // the identifier: an IP address or a DNS name
	Dir    string   // soju's tls.crt and tls.key
	State  string   // account.key, made on the first run
	ACME   string   // the CA's directory
	HTTP   string   // the http-01 listener's address
	Reload []string // run after a new certificate is in; none in tests
	Force  bool
	Steps  bool
	Out    io.Writer

	Client *http.Client     // for the CA; tests trust their own
	Now    func() time.Time // tests move time
}

func (r *renewer) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *renewer) say(format string, args ...any) {
	if r.Steps {
		fmt.Fprint(r.Out, "trout-step: ")
	}
	fmt.Fprintf(r.Out, format+"\n", args...)
}

func (r *renewer) fail(err error) {
	if r.Steps {
		fmt.Fprintln(r.Out, "trout-fail: "+strings.ReplaceAll(err.Error(), "\n", " "))
		return
	}
	fmt.Fprintln(os.Stderr, "trout-cert:", err)
}

// ca names the CA for messages: Let's Encrypt, or the directory's host.
func (r *renewer) ca() string {
	u, err := url.Parse(r.ACME)
	if err != nil || strings.HasSuffix(u.Hostname(), ".letsencrypt.org") {
		return "Let's Encrypt"
	}
	return u.Host
}

func (r *renewer) certPath() string { return filepath.Join(r.Dir, "tls.crt") }
func (r *renewer) keyPath() string  { return filepath.Join(r.Dir, "tls.key") }

// savedPath is the self-signed certificate trout made, kept from the
// first renewal so self-signed can put it back (Goguma remembers it by
// its SHA-1).
func (r *renewer) savedPath() string { return filepath.Join(r.Dir, "tls.crt.self-signed") }

// Renew gets a certificate for r.Host if the current one is due, or
// self-signed, or for another host, then reloads soju.
func (r *renewer) Renew(ctx context.Context) error {
	key, err := readKey(r.keyPath())
	if err != nil {
		return err
	}
	current, err := readCert(r.certPath())
	if err != nil {
		return err
	}
	if why := r.due(current, key); why == "" && !r.Force {
		half := current.NotBefore.Add(current.NotAfter.Sub(current.NotBefore) / 2)
		r.say("The certificate for %s is good until %s; it's renewed after %s", r.Host, stamp(current.NotAfter), stamp(half))
		return nil
	} else if why != "" {
		r.say("Getting %s a certificate from %s: %s", r.Host, r.ca(), why)
	}
	chain, err := r.obtain(ctx, key)
	if err != nil {
		return err
	}
	leaf, err := x509.ParseCertificate(chain[0])
	if err != nil {
		return err
	}
	if selfSigned(current) {
		if err := copyFile(r.certPath(), r.savedPath()); err != nil {
			return err
		}
	}
	var buf bytes.Buffer
	for _, der := range chain {
		pem.Encode(&buf, &pem.Block{Type: "CERTIFICATE", Bytes: der})
	}
	if err := replaceFile(r.certPath(), buf.Bytes()); err != nil {
		return err
	}
	r.say("Got it: %s vouches for %s until %s", issuerName(leaf), r.Host, stamp(leaf.NotAfter))
	return r.reload()
}

// due says why the certificate should be renewed, or "" if it's fine.
func (r *renewer) due(c *x509.Certificate, key crypto.Signer) string {
	switch {
	case selfSigned(c):
		return "it has the self-signed one trout made"
	case !samePublicKey(c.PublicKey, key.Public()):
		return "the certificate isn't for soju's key"
	case c.VerifyHostname(r.Host) != nil:
		return "the certificate is for another address"
	case r.now().After(c.NotBefore.Add(c.NotAfter.Sub(c.NotBefore) / 2)):
		return "half its time is up (it ends " + stamp(c.NotAfter) + ")"
	}
	return ""
}

// obtain runs the ACME order for r.Host with soju's key.
func (r *renewer) obtain(ctx context.Context, key crypto.Signer) ([][]byte, error) {
	account, err := r.accountKey()
	if err != nil {
		return nil, err
	}
	client := &acme.Client{Key: account, DirectoryURL: r.ACME, HTTPClient: r.Client, UserAgent: "trout-cert"}
	dir, err := client.Discover(ctx)
	if err != nil {
		return nil, fmt.Errorf("couldn't reach %s: %w", r.ca(), err)
	}
	// No contact address: Let's Encrypt no longer emails about expiry, and
	// doesn't keep addresses with accounts.
	if _, err := client.Register(ctx, &acme.Account{}, acme.AcceptTOS); err != nil && !errors.Is(err, acme.ErrAccountAlreadyExists) {
		return nil, fmt.Errorf("couldn't make a %s account: %w", r.ca(), err)
	}

	ids := acme.DomainIDs(r.Host)
	var opts []acme.OrderOption
	if ip := net.ParseIP(r.Host); ip != nil {
		ids = acme.IPIDs(r.Host)
		if _, ok := dir.Profiles[shortlived]; ok {
			opts = append(opts, acme.WithOrderIssuanceProfile(shortlived))
		}
	}
	order, err := client.AuthorizeOrder(ctx, ids, opts...)
	if err != nil {
		return nil, r.explain(err)
	}

	answers := map[string]string{}
	var mu sync.Mutex
	srv, err := r.serveTokens(func(token string) (string, bool) {
		mu.Lock()
		defer mu.Unlock()
		a, ok := answers[token]
		return a, ok
	})
	if err != nil {
		return nil, err
	}
	defer srv.Close()
	for _, u := range order.AuthzURLs {
		z, err := client.GetAuthorization(ctx, u)
		if err != nil {
			return nil, r.explain(err)
		}
		if z.Status == acme.StatusValid {
			continue
		}
		var chal *acme.Challenge
		for _, c := range z.Challenges {
			if c.Type == "http-01" {
				chal = c
			}
		}
		if chal == nil {
			return nil, fmt.Errorf("%s offered no http-01 check for %s", r.ca(), z.Identifier.Value)
		}
		answer, err := client.HTTP01ChallengeResponse(chal.Token)
		if err != nil {
			return nil, err
		}
		mu.Lock()
		answers[chal.Token] = answer
		mu.Unlock()
		r.say("%s is checking this server over port 80...", r.ca())
		if _, err := client.Accept(ctx, chal); err != nil {
			return nil, r.explain(err)
		}
		if _, err := client.WaitAuthorization(ctx, z.URI); err != nil {
			return nil, r.explain(err)
		}
	}
	if _, err := client.WaitOrder(ctx, order.URI); err != nil { // ready to finalize
		return nil, r.explain(err)
	}
	csr, err := r.request(key)
	if err != nil {
		return nil, err
	}
	chain, _, err := client.CreateCertFromOrder(ctx, order, csr, true)
	if err != nil {
		return nil, r.explain(err)
	}
	if len(chain) == 0 {
		return nil, fmt.Errorf("%s sent no certificate", r.ca())
	}
	return chain, nil
}

// serveTokens answers the CA's http-01 requests until it's closed.
func (r *renewer) serveTokens(answer func(token string) (string, bool)) (*http.Server, error) {
	ln, err := net.Listen("tcp", r.HTTP)
	if err != nil {
		if who := listening(r.HTTP); who != "" {
			return nil, fmt.Errorf("something else on this server uses port 80 (%s), and %s needs it for a moment every few days", who, r.ca())
		}
		return nil, fmt.Errorf("can't answer %s on port 80: %w", r.ca(), err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/acme-challenge/{token}", func(w http.ResponseWriter, req *http.Request) {
		if a, ok := answer(req.PathValue("token")); ok {
			io.WriteString(w, a)
			return
		}
		http.NotFound(w, req)
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go srv.Serve(ln)
	return srv, nil
}

// listening names what listens on addr's port, if ss can tell.
func listening(addr string) string {
	_, port, _ := net.SplitHostPort(addr)
	out, err := exec.Command("ss", "-Hltnp", "sport = :"+port).Output()
	if err != nil {
		return ""
	}
	line, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	if _, users, ok := strings.Cut(line, "users:((\""); ok {
		name, _, _ := strings.Cut(users, "\"")
		return name
	}
	return ""
}

// explain turns the CA's problems into what to do about them.
func (r *renewer) explain(err error) error {
	if ae, ok := errors.AsType[*acme.AuthorizationError](err); ok && len(ae.Errors) > 0 {
		err = ae.Errors[0]
	}
	if oe, ok := errors.AsType[*acme.OrderError](err); ok && oe.Problem != nil {
		err = oe.Problem
	}
	e, ok := errors.AsType[*acme.Error](err)
	if !ok {
		return err
	}
	if d, limited := acme.RateLimit(e); limited {
		msg := r.ca() + " says not so often: " + e.Detail
		if d > 0 {
			msg += " (try again in " + d.Round(time.Minute).String() + ")"
		}
		return errors.New(msg)
	}
	switch strings.TrimPrefix(e.ProblemType, "urn:ietf:params:acme:error:") {
	case "connection", "unauthorized", "incorrectResponse":
		return fmt.Errorf("%s couldn't fetch its token from %s on port 80 (%s). Allow TCP port 80 from anywhere, "+
			"in this server's firewall and at your provider (Hetzner: Firewalls; Google Cloud: VPC firewall rules), then try again",
			r.ca(), r.Host, e.Detail)
	case "rejectedIdentifier", "malformed":
		return fmt.Errorf("%s won't vouch for %s: %s", r.ca(), r.Host, e.Detail)
	}
	return fmt.Errorf("%s said: %s", r.ca(), e.Detail)
}

// request is the CSR for r.Host with soju's key.
func (r *renewer) request(key crypto.Signer) ([]byte, error) {
	tmpl := &x509.CertificateRequest{}
	if ip := net.ParseIP(r.Host); ip != nil {
		tmpl.IPAddresses = []net.IP{ip}
	} else {
		tmpl.Subject = pkix.Name{CommonName: r.Host}
		tmpl.DNSNames = []string{r.Host}
	}
	return x509.CreateCertificateRequest(rand.Reader, tmpl, key)
}

// accountKey is the ACME account's key, made on the first run.
func (r *renewer) accountKey() (crypto.Signer, error) {
	p := filepath.Join(r.State, "account.key")
	if k, err := readKey(p); err == nil {
		return k, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalECPrivateKey(k)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(r.State, 0o700); err != nil {
		return nil, err
	}
	return k, os.WriteFile(p, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}), 0o600)
}

// reload has soju pick up the new certificate: SIGHUP reloads it without
// dropping anyone. soju not running isn't a failure: it loads the
// certificate when it starts.
func (r *renewer) reload() error {
	if len(r.Reload) == 0 {
		return nil
	}
	out, err := exec.Command(r.Reload[0], r.Reload[1:]...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("the certificate is in, but reloading soju failed: %s", cmp.Or(strings.TrimSpace(string(out)), err.Error()))
	}
	r.say("soju has the new certificate")
	return nil
}

// SelfSigned puts back the self-signed certificate trout made, or makes
// one for soju's key, and reloads soju.
func (r *renewer) SelfSigned() error {
	if data, err := os.ReadFile(r.savedPath()); err == nil {
		if err := replaceFile(r.certPath(), data); err != nil {
			return err
		}
		os.Remove(r.savedPath())
		r.say("Put back the self-signed certificate")
		return r.reload()
	}
	key, err := readKey(r.keyPath())
	if err != nil {
		return err
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(r.now().UnixNano()),
		Subject:      pkix.Name{CommonName: r.Host},
		NotBefore:    r.now().Add(-time.Hour),
		NotAfter:     r.now().AddDate(30, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	if ip := net.ParseIP(r.Host); ip != nil {
		tmpl.IPAddresses = []net.IP{ip}
	} else {
		tmpl.DNSNames = []string{r.Host}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, key.Public(), key)
	if err != nil {
		return err
	}
	if err := replaceFile(r.certPath(), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})); err != nil {
		return err
	}
	r.say("Made a new self-signed certificate for soju's key")
	return r.reload()
}

func readKey(p string) (crypto.Signer, error) {
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("%s has no key in it", p)
	}
	if k, err := x509.ParseECPrivateKey(block.Bytes); err == nil {
		return k, nil
	}
	if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return k, nil
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("can't read the key in %s: %w", p, err)
	}
	s, ok := k.(crypto.Signer)
	if !ok {
		return nil, fmt.Errorf("can't sign with the key in %s", p)
	}
	return s, nil
}

// readCert is the first certificate in p.
func readCert(p string) (*x509.Certificate, error) {
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("%s has no certificate in it", p)
	}
	return x509.ParseCertificate(block.Bytes)
}

// selfSigned: c signed itself (trout's own), rather than a CA signing it.
func selfSigned(c *x509.Certificate) bool {
	return bytes.Equal(c.RawIssuer, c.RawSubject) && c.CheckSignature(c.SignatureAlgorithm, c.RawTBSCertificate, c.Signature) == nil
}

func samePublicKey(a, b crypto.PublicKey) bool {
	switch k := a.(type) {
	case *ecdsa.PublicKey:
		return k.Equal(b)
	case *rsa.PublicKey:
		return k.Equal(b)
	}
	return false
}

func issuerName(c *x509.Certificate) string {
	if len(c.Issuer.Organization) > 0 {
		return c.Issuer.Organization[0]
	}
	return c.Issuer.CommonName
}

func stamp(t time.Time) string { return t.UTC().Format("2006-01-02 15:04 UTC") }

// replaceFile writes data over p in one step, keeping p's mode and owner
// (root:soju, 0640, so soju can read it).
func replaceFile(p string, data []byte) error {
	st, err := os.Stat(p)
	if err != nil {
		return err
	}
	tmp := p + ".new"
	if err := os.WriteFile(tmp, data, st.Mode().Perm()); err != nil {
		return err
	}
	if err := chown(tmp, st); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, p)
}

func copyFile(from, to string) error {
	data, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	st, err := os.Stat(from)
	if err != nil {
		return err
	}
	if err := os.WriteFile(to, data, st.Mode().Perm()); err != nil {
		return err
	}
	return chown(to, st)
}
