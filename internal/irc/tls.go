package irc

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
)

// ErrLogin is a failed SASL login: a wrong user name or password.
var ErrLogin = errors.New("login failed")

// ParseAddress reads a bouncer address the way soju writes them:
// ircs://host[:port] for TLS (port 6697 by default) or
// irc+insecure://host[:port] for plain text (port 6667). A bare host or
// host:port means TLS.
func ParseAddress(s string) (hostport string, useTLS bool, err error) {
	s = strings.TrimSpace(s)
	if !strings.Contains(s, "://") {
		s = "ircs://" + s
	}
	u, err := url.Parse(s)
	if err != nil {
		return "", false, fmt.Errorf("can't read the address %q", s)
	}
	port := "6697"
	switch u.Scheme {
	case "ircs":
		useTLS = true
	case "irc+insecure":
		port = "6667"
	case "irc":
		return "", false, errors.New("say ircs:// for TLS or irc+insecure:// for plain text, not irc://")
	default:
		return "", false, fmt.Errorf("unknown address scheme %s:// (use ircs:// or irc+insecure://)", u.Scheme)
	}
	if u.Hostname() == "" || (u.Path != "" && u.Path != "/") {
		return "", false, fmt.Errorf("the address %q should be a host and optional port", s)
	}
	if p := u.Port(); p != "" {
		port = p
	}
	return net.JoinHostPort(u.Hostname(), port), useTLS, nil
}

// Fingerprint identifies a certificate's public key, SSH-style:
// "SHA256:" and the unpadded base64 of the key's SHA-256. It names the
// key rather than the certificate, so renewing a certificate with the same
// key keeps a pin valid.
func Fingerprint(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	return "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:])
}

// CertError is a certificate trout won't use without asking: no known
// authority signed it and either nothing is pinned (Pinned is empty) or
// the pin is for a different key.
type CertError struct {
	Addr        string
	Fingerprint string // the key the server presented
	Pinned      string
	Err         error // why the system's authorities didn't accept it
}

func (e *CertError) Error() string {
	if e.Pinned != "" {
		return fmt.Sprintf("%s presented key %s, but %s is pinned", e.Addr, e.Fingerprint, e.Pinned)
	}
	return fmt.Sprintf("%s presented an untrusted certificate (key %s): %v", e.Addr, e.Fingerprint, e.Err)
}

func (e *CertError) Unwrap() error { return e.Err }

// TLSConfig verifies the server at cfg.Addr against the system's
// authorities, then falls back to the pinned key cfg.Pin, like a browser
// with an SSH twist. The bouncer's HTTP file host uses it too.
func (cfg Config) TLSConfig() *tls.Config {
	host, _, _ := net.SplitHostPort(cfg.Addr)
	var certs []tls.Certificate
	if cfg.ClientCert != nil {
		certs = []tls.Certificate{*cfg.ClientCert}
	}
	return &tls.Config{
		ServerName:   host,
		Certificates: certs,
		// Verification happens in VerifyConnection, which also sees
		// certificates no authority signed.
		InsecureSkipVerify: true,
		VerifyConnection: func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return errors.New("the server sent no certificate")
			}
			leaf := cs.PeerCertificates[0]
			inter := x509.NewCertPool()
			for _, ct := range cs.PeerCertificates[1:] {
				inter.AddCert(ct)
			}
			_, err := leaf.Verify(x509.VerifyOptions{DNSName: host, Intermediates: inter})
			if err == nil {
				return nil
			}
			fp := Fingerprint(leaf)
			if cfg.Pin == fp {
				return nil
			}
			return &CertError{Addr: cfg.Addr, Fingerprint: fp, Pinned: cfg.Pin, Err: err}
		},
	}
}
