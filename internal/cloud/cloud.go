// Package cloud makes a small server for a bouncer at a cloud provider:
// list the regions with their cheapest plan and price, create a Debian
// server with your SSH key, wait for its public address, and delete it.
// What goes on the server is internal/install's job, over ssh as root.
package cloud

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Provider is one cloud.
type Provider interface {
	ID() string   // "hetzner", "digitalocean": config.toml's and the credentials file's
	Name() string // for people
	Regions(ctx context.Context) ([]Region, error)
	// Create makes a server and returns once it's running with a public
	// IPv4 address. key is an OpenSSH public key line, for root.
	Create(ctx context.Context, r Region, name, key string) (Server, error)
	Delete(ctx context.Context, id string) error
}

// Region is where a server can go, with the cheapest plan there.
type Region struct {
	ID, Label  string
	Plan       string  // the provider's name for the server type or size
	Image      string  // Debian, for the plan's architecture
	Monthly    float64 // what it costs a month, its public IPv4 included
	Currency   string  // "USD", "EUR"
	PlanDetail string  // "2 CPUs, 4 GB, arm64"
}

// Price is what Monthly says, for people: "$4.60/month".
func (r Region) Price() string {
	sym := map[string]string{"USD": "$", "EUR": "€"}[r.Currency]
	if sym == "" {
		return fmt.Sprintf("%.2f %s/month", r.Monthly, r.Currency)
	}
	return fmt.Sprintf("%s%.2f/month", sym, r.Monthly)
}

// Server is a server Create made.
type Server struct {
	ID   string
	IPv4 string
}

// Fingerprint is an OpenSSH public key's MD5 fingerprint, aa:bb:..., the
// way both providers name keys.
func Fingerprint(key string) (string, error) {
	f := strings.Fields(key)
	if len(f) < 2 {
		return "", errors.New("that isn't an OpenSSH public key")
	}
	blob, err := base64.StdEncoding.DecodeString(f[1])
	if err != nil {
		return "", fmt.Errorf("that isn't an OpenSSH public key: %w", err)
	}
	sum := md5.Sum(blob)
	var b strings.Builder
	for i, c := range sum {
		if i > 0 {
			b.WriteByte(':')
		}
		fmt.Fprintf(&b, "%02x", c)
	}
	return b.String(), nil
}

// api is a JSON API with a bearer token.
type api struct {
	base, token string
	client      *http.Client
}

// APIError is the provider saying no.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string { return fmt.Sprintf("%s (HTTP %d)", e.Message, e.Status) }

// ErrToken is a token the provider doesn't take.
var ErrToken = errors.New("the provider didn't accept that API token")

func (a api) do(ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.base+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+a.token)
	req.Header.Set("Content-Type", "application/json")
	client := a.client
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return ErrToken
	}
	if resp.StatusCode >= 300 {
		return &APIError{Status: resp.StatusCode, Message: errorMessage(data)}
	}
	if out == nil || len(data) == 0 {
		return nil
	}
	return json.Unmarshal(data, out)
}

// errorMessage digs the message out of either provider's error body.
func errorMessage(data []byte) string {
	var e struct {
		Message string `json:"message"` // DigitalOcean
		Error   struct {
			Message string `json:"message"`
		} `json:"error"` // Hetzner
	}
	json.Unmarshal(data, &e)
	switch {
	case e.Error.Message != "":
		return e.Error.Message
	case e.Message != "":
		return e.Message
	}
	return strings.TrimSpace(string(data))
}

// poll calls check every interval until it says done or ctx ends.
func poll(ctx context.Context, interval time.Duration, check func() (bool, error)) error {
	for {
		done, err := check()
		if err != nil || done {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
	}
}

// Labels and tags mark what trout made.
const Tag = "trout"
