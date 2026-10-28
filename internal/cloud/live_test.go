//go:build cloud

// The providers for real, with the tokens in tmp/cloud.env: list regions,
// then (TestCloudServer) create the cheapest server, check ssh reaches it
// as root, and delete it. Costs a few cents per run.
//
//	go test -tags cloud ./internal/cloud -run TestCloudRegions -v
//	go test -tags cloud ./internal/cloud -run TestCloudServer -v
package cloud

import (
	"bufio"
	"cmp"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func providers(t *testing.T) []Provider {
	t.Helper()
	f, err := os.Open(filepath.Join("..", "..", "tmp", "cloud.env"))
	if err != nil {
		t.Skip(err)
	}
	defer f.Close()
	env := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if k, v, ok := strings.Cut(sc.Text(), "="); ok && !strings.HasPrefix(k, "#") {
			env[k] = v
		}
	}
	var ps []Provider
	if tok := env["HCLOUD_TOKEN"]; tok != "" {
		ps = append(ps, NewHetzner(tok))
	}
	if tok := env["DO_TOKEN"]; tok != "" {
		ps = append(ps, NewDigitalOcean(tok))
	}
	if p := env["GCP_PROJECT"]; p != "" {
		ps = append(ps, NewGCloud(p))
	}
	if only := os.Getenv("TROUT_CLOUD"); only != "" { // just one: hetzner, digitalocean, gcloud
		ps = slices.DeleteFunc(ps, func(p Provider) bool { return p.ID() != only })
	}
	return ps
}

func TestCloudRegions(t *testing.T) {
	for _, p := range providers(t) {
		rs, err := p.Regions(context.Background())
		if err != nil || len(rs) == 0 {
			t.Fatalf("%s: %v %v", p.Name(), rs, err)
		}
		for _, r := range rs {
			t.Logf("%s: %s (%s): %s, %s, %s", p.Name(), r.Label, r.ID, r.Plan, r.PlanDetail, r.Price())
		}
	}
}

func TestCloudServer(t *testing.T) {
	key, err := os.ReadFile(filepath.Join("..", "..", "tmp", "pve_ed25519.pub"))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range providers(t) {
		t.Run(p.ID(), func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			defer cancel()
			rs, err := p.Regions(ctx)
			if err != nil {
				t.Fatal(err)
			}
			r := rs[0] // the cheapest
			start := time.Now()
			s, err := p.Create(ctx, r, "trout-test-"+time.Now().Format("150405"), string(key))
			if s.ID != "" {
				t.Cleanup(func() {
					if err := p.Delete(context.Background(), s.ID); err != nil {
						t.Errorf("deleting %s server %s: %v", p.Name(), s.ID, err)
					}
				})
			}
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("%s: server %s at %s in %s (%s), %s, up in %s", p.Name(), s.ID, s.IPv4, r.Label, r.Plan, r.Price(), time.Since(start).Round(time.Second))
			var out []byte
			for deadline := time.Now().Add(3 * time.Minute); ; time.Sleep(3 * time.Second) {
				out, err = exec.Command("ssh", "-i", filepath.Join("..", "..", "tmp", "pve_ed25519"), "-o", "BatchMode=yes",
					"-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null", "-o", "ConnectTimeout=5", "-o", "LogLevel=ERROR",
					cmp.Or(s.User, "root")+"@"+s.IPv4, "--", "sudo -n id -u; . /etc/os-release; echo $PRETTY_NAME; uname -m").CombinedOutput()
				if err == nil || time.Now().After(deadline) {
					break
				}
			}
			if err != nil || !strings.HasPrefix(string(out), "0\n") {
				t.Fatalf("ssh as root: %v\n%s", err, out)
			}
			t.Logf("%s: ssh as root after %s: %s", p.Name(), time.Since(start).Round(time.Second), strings.ReplaceAll(strings.TrimSpace(string(out)), "\n", ", "))
		})
	}
}
