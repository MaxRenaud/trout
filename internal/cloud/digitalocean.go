package cloud

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"time"
)

// DigitalOcean is DigitalOcean droplets (docs.digitalocean.com/reference/api),
// with a personal access token that can read and write droplets and SSH
// keys and read regions, sizes and images.
type DigitalOcean struct{ api }

// DigitalOceanReferral is trout's author's referral link: people who sign
// up with it get DigitalOcean credit, and so does the author once they
// spend some. Shown, labeled, where an account is needed.
const DigitalOceanReferral = "https://m.do.co/c/946c1115a344"

func NewDigitalOcean(token string) *DigitalOcean {
	return &DigitalOcean{api{base: "https://api.digitalocean.com/v2", token: token}}
}

func (d *DigitalOcean) ID() string   { return "digitalocean" }
func (d *DigitalOcean) Name() string { return "DigitalOcean" }

func (d *DigitalOcean) Regions(ctx context.Context) ([]Region, error) {
	var regions struct {
		Regions []struct {
			Slug, Name string
			Available  bool
		}
	}
	var sizes struct {
		Sizes []struct {
			Slug         string
			PriceMonthly float64 `json:"price_monthly"`
			Memory       int
			VCPUs        int
			Available    bool
			Regions      []string
		}
	}
	if err := d.do(ctx, "GET", "/regions?per_page=200", nil, &regions); err != nil {
		return nil, err
	}
	if err := d.do(ctx, "GET", "/sizes?per_page=200", nil, &sizes); err != nil {
		return nil, err
	}
	var out []Region
	for _, r := range regions.Regions {
		if !r.Available {
			continue
		}
		best := Region{ID: r.Slug, Label: r.Name, Currency: "USD", Image: "debian-13-x64"}
		for _, s := range sizes.Sizes {
			if !s.Available || !slices.Contains(s.Regions, r.Slug) || (best.Plan != "" && s.PriceMonthly >= best.Monthly) {
				continue
			}
			// A droplet's price includes its public IPv4.
			best.Plan, best.Monthly = s.Slug, s.PriceMonthly
			best.PlanDetail = fmt.Sprintf("%d CPU, %d MB, amd64", s.VCPUs, s.Memory)
		}
		if best.Plan != "" {
			out = append(out, best)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Monthly < out[j].Monthly })
	return out, nil
}

// key uploads the public key, or finds it when it's already there.
func (d *DigitalOcean) key(ctx context.Context, key string) (string, error) {
	fp, err := Fingerprint(key)
	if err != nil {
		return "", err
	}
	err = d.do(ctx, "GET", "/account/keys/"+url.PathEscape(fp), nil, nil)
	var ae *APIError
	if errors.As(err, &ae) && ae.Status == 404 {
		err = d.do(ctx, "POST", "/account/keys", map[string]string{"name": "trout " + fp[:11], "public_key": key}, nil)
	}
	return fp, err
}

type doDroplet struct {
	Droplet struct {
		ID       int64
		Status   string
		Networks struct {
			V4 []struct {
				IPAddress string `json:"ip_address"`
				Type      string
			}
		}
	}
}

func (d *DigitalOcean) Create(ctx context.Context, r Region, name, key string) (Server, error) {
	fp, err := d.key(ctx, key)
	if err != nil {
		return Server{}, fmt.Errorf("uploading your SSH key: %w", err)
	}
	var dr doDroplet
	err = d.do(ctx, "POST", "/droplets", map[string]any{
		"name": name, "region": r.ID, "size": r.Plan, "image": r.Image,
		"ssh_keys": []string{fp}, "tags": []string{Tag}, "ipv6": true,
	}, &dr)
	if err != nil {
		return Server{}, err
	}
	id := strconv.FormatInt(dr.Droplet.ID, 10)
	var ip string
	err = poll(ctx, 3*time.Second, func() (bool, error) {
		if err := d.do(ctx, "GET", "/droplets/"+id, nil, &dr); err != nil {
			return false, err
		}
		for _, n := range dr.Droplet.Networks.V4 {
			if n.Type == "public" {
				ip = n.IPAddress
			}
		}
		return dr.Droplet.Status == "active" && ip != "", nil
	})
	return Server{ID: id, IPv4: ip}, err
}

func (d *DigitalOcean) Delete(ctx context.Context, id string) error {
	err := d.do(ctx, "DELETE", "/droplets/"+url.PathEscape(id), nil, nil)
	var ae *APIError
	if errors.As(err, &ae) && ae.Status == 404 {
		return nil // already gone
	}
	return err
}
