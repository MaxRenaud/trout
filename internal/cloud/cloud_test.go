package cloud

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// fakeAPI answers GETs by path with canned JSON, and anything without
// the right token with 401.
func fakeAPI(t *testing.T, routes map[string]string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer good" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		body, ok := routes[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestHetznerRegions(t *testing.T) {
	url := fakeAPI(t, map[string]string{
		"/locations": `{"locations":[{"name":"fsn1","city":"Falkenstein","country":"DE"},{"name":"ash","city":"Ashburn, VA","country":"US"}]}`,
		// cx23 is cheaper but no longer orderable in fsn1; cpx99 is deprecated.
		"/server_types": `{"server_types":[
			{"name":"cx23","architecture":"x86","cores":2,"memory":4,"prices":[{"location":"fsn1","price_monthly":{"gross":"5.00"}}],
			 "locations":[{"name":"fsn1","available":false}]},
			{"name":"cax11","architecture":"arm","cores":2,"memory":4,"prices":[{"location":"fsn1","price_monthly":{"gross":"6.99"}}],
			 "locations":[{"name":"fsn1","available":true}]},
			{"name":"cpx99","architecture":"x86","cores":1,"memory":1,"deprecated":true,"prices":[{"location":"ash","price_monthly":{"gross":"1.00"}}],
			 "locations":[{"name":"ash","available":true}]},
			{"name":"cpx11","architecture":"x86","cores":2,"memory":2,"prices":[{"location":"ash","price_monthly":{"gross":"20.49"}}],
			 "locations":[{"name":"ash","available":true,"deprecation":null}]}]}`,
		"/pricing": `{"pricing":{"currency":"USD","primary_ips":[{"type":"ipv4","prices":[{"location":"fsn1","price_monthly":{"gross":"0.60"}},{"location":"ash","price_monthly":{"gross":"0.60"}}]}]}}`,
	})
	h := NewHetzner("good")
	h.base = url
	rs, err := h.Regions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 2 || rs[0].ID != "fsn1" || rs[0].Plan != "cax11" || rs[0].Price() != "$7.59/month" ||
		rs[0].PlanDetail != "2 CPUs, 4 GB, arm64" || rs[1].Plan != "cpx11" || rs[1].Label != "Ashburn, VA, US" {
		t.Fatalf("%+v", rs)
	}
	h.token = "bad"
	if _, err := h.Regions(context.Background()); !errors.Is(err, ErrToken) {
		t.Fatalf("a bad token: %v", err)
	}
}

func TestDigitalOceanRegions(t *testing.T) {
	url := fakeAPI(t, map[string]string{
		"/regions": `{"regions":[{"slug":"fra1","name":"Frankfurt 1","available":true},{"slug":"old1","name":"Old 1","available":false}]}`,
		"/sizes": `{"sizes":[
			{"slug":"s-1vcpu-1gb","price_monthly":6,"memory":1024,"vcpus":1,"available":true,"regions":["fra1"]},
			{"slug":"s-1vcpu-512mb-10gb","price_monthly":4,"memory":512,"vcpus":1,"available":true,"regions":["fra1"]},
			{"slug":"gone","price_monthly":1,"memory":256,"vcpus":1,"available":false,"regions":["fra1"]}]}`,
	})
	d := NewDigitalOcean("good")
	d.base = url
	rs, err := d.Regions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 1 || rs[0].Plan != "s-1vcpu-512mb-10gb" || rs[0].Price() != "$4.00/month" || rs[0].Image != "debian-13-x64" {
		t.Fatalf("%+v", rs)
	}
}

// TestFingerprint matches ssh-keygen's MD5 fingerprint, which both
// providers name keys by.
func TestFingerprint(t *testing.T) {
	key := filepath.Join(t.TempDir(), "k")
	if err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", key).Run(); err != nil {
		t.Skip("no ssh-keygen:", err)
	}
	pub, _ := os.ReadFile(key + ".pub")
	out, err := exec.Command("ssh-keygen", "-l", "-E", "md5", "-f", key+".pub").Output()
	if err != nil {
		t.Fatal(err)
	}
	want := strings.TrimPrefix(strings.Fields(string(out))[1], "MD5:")
	if got, err := Fingerprint(string(pub)); err != nil || got != want {
		t.Fatalf("got %q, want %q (%v)", got, want, err)
	}
	if _, err := Fingerprint("not a key"); err == nil {
		t.Fatal("no error for junk")
	}
}

// TestGCloudCleansUp: when the VM can't be made after its IPv4 was
// reserved, the address is released, not left billing unattached.
func TestGCloudCleansUp(t *testing.T) {
	var ran []string
	g := &GCloud{Project: "p", Run: func(_ context.Context, args ...string) ([]byte, error) {
		cmd := strings.Join(args[:3], " ")
		ran = append(ran, cmd)
		switch cmd {
		case "compute addresses create":
			return []byte("192.0.2.9\n"), nil
		case "compute instances create":
			return nil, errors.New("gcloud compute instances: quota exceeded")
		}
		return nil, nil
	}}
	_, err := g.Create(context.Background(), Region{ID: "us-west1-b", Plan: "e2-micro", Image: "debian-13"}, "trout-x", "ssh-ed25519 AAAA")
	if err == nil || !strings.Contains(err.Error(), "quota") {
		t.Fatalf("got %v", err)
	}
	if !slices.Contains(ran, "compute addresses delete") {
		t.Fatalf("the address wasn't released: %q", ran)
	}
}

// TestHetznerUndo: a server that was made but never came up is deleted, so
// it isn't left billing.
func TestHetznerUndo(t *testing.T) {
	var deleted bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/ssh_keys":
			w.Write([]byte(`{"ssh_keys":[{"id":1}]}`))
		case r.Method == "POST" && r.URL.Path == "/servers":
			w.Write([]byte(`{"server":{"id":42,"status":"initializing"}}`))
		case r.Method == "GET" && r.URL.Path == "/servers/42":
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`{"error":{"message":"oops"}}`))
		case r.Method == "DELETE" && r.URL.Path == "/servers/42":
			deleted = true
		}
	}))
	defer srv.Close()
	h := NewHetzner("good")
	h.base = srv.URL
	s, err := h.Create(context.Background(), Region{ID: "fsn1", Plan: "cax11", Image: "debian-13"}, "trout-x", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExample")
	if err == nil || s.ID != "" || !deleted {
		t.Fatalf("got %+v, %v, deleted %v", s, err, deleted)
	}
}
