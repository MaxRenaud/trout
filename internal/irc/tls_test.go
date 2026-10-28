package irc

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha1"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"errors"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"
)

func TestParseAddress(t *testing.T) {
	for in, want := range map[string]string{
		"bnc.example.net":                 "bnc.example.net:6697 tls",
		"bnc.example.net:7000":            "bnc.example.net:7000 tls",
		"ircs://bnc.example.net":          "bnc.example.net:6697 tls",
		"ircs://[::1]:6697":               "[::1]:6697 tls",
		"irc+insecure://127.0.0.1:6667":   "127.0.0.1:6667 plain",
		"  irc+insecure://localhost  ":    "localhost:6667 plain",
		"irc://bnc.example.net":           "error",
		"https://bnc.example.net":         "error",
		"ircs://":                         "error",
		"ircs://bnc.example.net/somepath": "error",
	} {
		hp, useTLS, err := ParseAddress(in)
		got := "error"
		if err == nil {
			got = hp + map[bool]string{true: " tls", false: " plain"}[useTLS]
		}
		if got != want {
			t.Errorf("ParseAddress(%q) = %s (%v), want %s", in, got, err, want)
		}
	}
}

// TestDialTLS connects to a server with a self-signed certificate: refused
// with its fingerprint until it's pinned, then fine, and refused again if
// the pin is for another key.
func TestDialTLS(t *testing.T) {
	cert := selfSigned(t)
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}})
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			nc, err := ln.Accept()
			if err != nil {
				return
			}
			go welcome(nc)
		}
	}()

	dial := func(pin string) error {
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()
		c, err := Dial(ctx, Config{Addr: ln.Addr().String(), TLS: true, Pin: pin, Nick: "trout", User: "trout"})
		if err == nil {
			c.Close()
		}
		return err
	}

	var ce *CertError
	if err := dial(""); !errors.As(err, &ce) || ce.Pinned != "" {
		t.Fatalf("unpinned self-signed certificate: %v, want a CertError", err)
	}
	leaf, _ := x509.ParseCertificate(cert.Certificate[0])
	if want := Fingerprint(leaf); ce.Fingerprint != want || !strings.HasPrefix(want, "SHA256:") {
		t.Fatalf("fingerprint %q, want %q", ce.Fingerprint, want)
	}
	if err := dial(ce.Fingerprint); err != nil {
		t.Fatalf("with the key pinned: %v", err)
	}
	if err := dial("SHA256:someotherkey"); !errors.As(err, &ce) || ce.Pinned != "SHA256:someotherkey" {
		t.Fatalf("with another key pinned: %v, want a CertError naming the pin", err)
	}
}

// welcome registers anyone without capabilities.
func welcome(nc net.Conn) {
	defer nc.Close()
	r := bufio.NewReader(nc)
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		switch strings.TrimSpace(line) {
		case "CAP LS 302":
			nc.Write([]byte(":soju CAP * LS :\r\n"))
		case "CAP END":
			nc.Write([]byte(":soju 001 trout :Welcome\r\n"))
		}
	}
}

func selfSigned(t *testing.T) tls.Certificate {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "soju.test"},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1)},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// TestCertSHA1 is the certificate's SHA-1 as Goguma shows it, only when
// the pin matches.
func TestCertSHA1(t *testing.T) {
	cert := selfSigned(t)
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}})
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			nc, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { nc.(*tls.Conn).Handshake(); nc.Close() }()
		}
	}()
	leaf, _ := x509.ParseCertificate(cert.Certificate[0])
	sum := sha1.Sum(cert.Certificate[0])
	got, err := CertSHA1(t.Context(), Config{Addr: ln.Addr().String(), Pin: Fingerprint(leaf)})
	if err != nil || got != hex.EncodeToString(sum[:]) {
		t.Fatalf("got %q, %v", got, err)
	}
	if _, err := CertSHA1(t.Context(), Config{Addr: ln.Addr().String(), Pin: "SHA256:other"}); err == nil {
		t.Fatal("no error with the wrong pin")
	}
}
