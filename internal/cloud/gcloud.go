package cloud

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strings"
)

// GCloud is Google Compute Engine through the gcloud command (logged in,
// with a project), like trout uses the system ssh: no OAuth app of
// trout's own. It makes the free tier's e2-micro in one of the regions
// where that's free, with a reserved IPv4 (Google bills about $3.65 a
// month for it) that Delete releases with the server.
type GCloud struct {
	Project string
	// Run runs gcloud with args and returns its output; tests replace it.
	Run func(ctx context.Context, args ...string) ([]byte, error)
}

// gcloudRegions are where the free tier's e2-micro is free.
var gcloudRegions = []struct{ id, label string }{
	{"us-west1", "Oregon, US"}, {"us-central1", "Iowa, US"}, {"us-east1", "South Carolina, US"},
}

// gcloudIPv4 is an external IPv4's monthly price, $0.005 an hour.
const gcloudIPv4 = 3.65

// gcloudFirewall is the rule letting IRC (6697) and file uploads (8443)
// reach servers tagged with it.
const gcloudFirewall = "trout-bouncer"

// NewGCloud uses project, or gcloud's own when it's empty.
func NewGCloud(project string) *GCloud { return &GCloud{Project: project} }

func (g *GCloud) ID() string   { return "gcloud" }
func (g *GCloud) Name() string { return "Google Cloud" }

func (g *GCloud) gcloud(ctx context.Context, args ...string) ([]byte, error) {
	if g.Run != nil {
		return g.Run(ctx, args...)
	}
	cmd := exec.CommandContext(ctx, "gcloud", append(args, "--quiet")...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if errors.Is(err, exec.ErrNotFound) {
		return nil, errors.New("Google Cloud needs the gcloud command (cloud.google.com/sdk), logged in with gcloud auth login")
	}
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if i := strings.LastIndex(msg, "ERROR: "); i >= 0 {
			msg = msg[i+len("ERROR: "):]
		}
		return out, fmt.Errorf("gcloud %s: %s", args[0]+" "+args[1], msg)
	}
	return out, nil
}

func (g *GCloud) project(ctx context.Context) (string, error) {
	if g.Project != "" {
		return g.Project, nil
	}
	out, err := g.gcloud(ctx, "config", "get-value", "project")
	g.Project = strings.TrimSpace(string(out))
	if err == nil && g.Project == "" {
		err = errors.New("gcloud has no project: gcloud config set project <yours>")
	}
	return g.Project, err
}

func (g *GCloud) Regions(ctx context.Context) ([]Region, error) {
	p, err := g.project(ctx)
	if err != nil {
		return nil, err
	}
	out, err := g.gcloud(ctx, "compute", "zones", "list", "--project", p, "--filter", "status=UP", "--format", "json(name,region)")
	if err != nil {
		return nil, err
	}
	var zones []struct{ Name, Region string }
	if err := json.Unmarshal(out, &zones); err != nil {
		return nil, err
	}
	slices.SortFunc(zones, func(a, b struct{ Name, Region string }) int { return strings.Compare(a.Name, b.Name) })
	var rs []Region
	for _, r := range gcloudRegions {
		for _, z := range zones {
			if strings.HasSuffix(z.Region, "/"+r.id) {
				rs = append(rs, Region{ID: z.Name, Label: r.label + " (" + z.Name + ")", Plan: "e2-micro",
					Image: "debian-13", Monthly: gcloudIPv4, Currency: "USD",
					PlanDetail: "2 shared CPUs, 1 GB, amd64; the free tier"})
				break
			}
		}
	}
	return rs, nil
}

// region is a zone's region: us-west1-b's us-west1.
func region(zone string) string { return zone[:strings.LastIndex(zone, "-")] }

func (g *GCloud) Create(ctx context.Context, r Region, name, key string) (Server, error) {
	p, err := g.project(ctx)
	if err != nil {
		return Server{}, err
	}
	if _, err := g.gcloud(ctx, "compute", "firewall-rules", "describe", gcloudFirewall, "--project", p, "--format", "value(name)"); err != nil {
		if _, err := g.gcloud(ctx, "compute", "firewall-rules", "create", gcloudFirewall, "--project", p,
			"--allow", "tcp:6697,tcp:8443", "--target-tags", gcloudFirewall,
			"--description", "trout's bouncers: IRC over TLS and file uploads"); err != nil {
			return Server{}, err
		}
	}
	out, err := g.gcloud(ctx, "compute", "addresses", "create", name, "--project", p, "--region", region(r.ID),
		"--format", "value(address)")
	if err != nil {
		return Server{}, err
	}
	ip := strings.TrimSpace(string(out))
	if ip == "" { // older gcloud prints nothing on create
		out, err = g.gcloud(ctx, "compute", "addresses", "describe", name, "--project", p, "--region", region(r.ID), "--format", "value(address)")
		ip = strings.TrimSpace(string(out))
	}
	s := Server{ID: p + "/" + r.ID + "/" + name, IPv4: ip, User: "trout"}
	if err == nil {
		// Google's images don't let root in over ssh; its guest agent makes
		// the ssh-keys user, with passwordless sudo.
		_, err = g.gcloud(ctx, "compute", "instances", "create", name, "--project", p, "--zone", r.ID,
			"--machine-type", r.Plan, "--image-family", r.Image, "--image-project", "debian-cloud",
			"--boot-disk-size", "30GB", "--boot-disk-type", "pd-standard", "--address", ip,
			"--tags", gcloudFirewall, "--labels", Tag+"=bouncer", "--metadata", "ssh-keys=trout:"+key)
	}
	if err != nil {
		g.Delete(context.WithoutCancel(ctx), s.ID) // not a server and an address left billing
		return Server{}, err
	}
	return s, nil
}

func (g *GCloud) Delete(ctx context.Context, id string) error {
	parts := strings.Split(id, "/")
	if len(parts) != 3 {
		return fmt.Errorf("can't read the server %q", id)
	}
	p, zone, name := parts[0], parts[1], parts[2]
	var errs []error
	if _, err := g.gcloud(ctx, "compute", "instances", "delete", name, "--project", p, "--zone", zone); err != nil && !notFound(err) {
		errs = append(errs, err)
	}
	// Its reserved address would go on billing, unattached.
	if _, err := g.gcloud(ctx, "compute", "addresses", "delete", name, "--project", p, "--region", region(zone)); err != nil && !notFound(err) {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func notFound(err error) bool { return strings.Contains(err.Error(), "was not found") }
