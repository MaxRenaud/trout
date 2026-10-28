package cloud

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"time"
)

// Hetzner is Hetzner Cloud (docs.hetzner.cloud), with an API token from a
// project's Security > API tokens (read and write).
type Hetzner struct{ api }

func NewHetzner(token string) *Hetzner {
	return &Hetzner{api{base: "https://api.hetzner.cloud/v1", token: token}}
}

func (h *Hetzner) ID() string   { return "hetzner" }
func (h *Hetzner) Name() string { return "Hetzner Cloud" }

type hzPrice struct {
	Location string `json:"location"`
	Monthly  struct {
		Gross string `json:"gross"`
	} `json:"price_monthly"`
}

func (h *Hetzner) Regions(ctx context.Context) ([]Region, error) {
	var locs struct {
		Locations []struct {
			Name, City, Country string
		} `json:"locations"`
	}
	var types struct {
		ServerTypes []struct {
			Name, Architecture string
			Cores              int
			Memory             float64
			Deprecated         bool
			Prices             []hzPrice
			Locations          []hzTypeLocation
		} `json:"server_types"`
	}
	var pricing struct {
		Pricing struct {
			Currency   string
			PrimaryIPs []struct {
				Type   string
				Prices []hzPrice
			} `json:"primary_ips"`
		}
	}
	if err := h.do(ctx, "GET", "/locations", nil, &locs); err != nil {
		return nil, err
	}
	if err := h.do(ctx, "GET", "/server_types?per_page=50", nil, &types); err != nil {
		return nil, err
	}
	if err := h.do(ctx, "GET", "/pricing", nil, &pricing); err != nil {
		return nil, err
	}
	ipv4 := map[string]float64{}
	for _, ip := range pricing.Pricing.PrimaryIPs {
		if ip.Type == "ipv4" {
			for _, p := range ip.Prices {
				ipv4[p.Location], _ = strconv.ParseFloat(p.Monthly.Gross, 64)
			}
		}
	}
	var out []Region
	for _, l := range locs.Locations {
		best := Region{ID: l.Name, Label: l.City + ", " + l.Country, Currency: pricing.Pricing.Currency}
		for _, t := range types.ServerTypes {
			if t.Deprecated || !orderable(t.Locations, l.Name) {
				continue
			}
			for _, p := range t.Prices {
				price, _ := strconv.ParseFloat(p.Monthly.Gross, 64)
				if p.Location != l.Name || (best.Plan != "" && price+ipv4[l.Name] >= best.Monthly) {
					continue
				}
				arch := map[string]string{"x86": "amd64", "arm": "arm64"}[t.Architecture]
				best.Plan, best.Monthly = t.Name, price+ipv4[l.Name]
				best.Image = "debian-13" // Hetzner picks the image's architecture by server type
				best.PlanDetail = fmt.Sprintf("%d CPUs, %g GB, %s", t.Cores, t.Memory, arch)
			}
		}
		if best.Plan != "" {
			out = append(out, best)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Monthly < out[j].Monthly })
	return out, nil
}

// hzTypeLocation is whether a server type can be ordered at a location:
// available, and not on its way out.
type hzTypeLocation struct {
	Name        string
	Available   bool
	Deprecation *struct{} `json:"deprecation"`
}

func orderable(locs []hzTypeLocation, name string) bool {
	for _, l := range locs {
		if l.Name == name {
			return l.Available && l.Deprecation == nil
		}
	}
	return false
}

// key uploads the public key, or finds it when it's already there.
func (h *Hetzner) key(ctx context.Context, key string) (int64, error) {
	fp, err := Fingerprint(key)
	if err != nil {
		return 0, err
	}
	var found struct {
		SSHKeys []struct{ ID int64 } `json:"ssh_keys"`
	}
	if err := h.do(ctx, "GET", "/ssh_keys?fingerprint="+url.QueryEscape(fp), nil, &found); err != nil {
		return 0, err
	}
	if len(found.SSHKeys) > 0 {
		return found.SSHKeys[0].ID, nil
	}
	var made struct {
		SSHKey struct{ ID int64 } `json:"ssh_key"`
	}
	err = h.do(ctx, "POST", "/ssh_keys", map[string]any{"name": "trout " + fp[:11], "public_key": key,
		"labels": map[string]string{Tag: "key"}}, &made)
	return made.SSHKey.ID, err
}

type hzServer struct {
	Server struct {
		ID        int64
		Status    string
		PublicNet struct {
			IPv4 struct{ IP string } `json:"ipv4"`
		} `json:"public_net"`
	}
}

func (h *Hetzner) Create(ctx context.Context, r Region, name, key string) (Server, error) {
	kid, err := h.key(ctx, key)
	if err != nil {
		return Server{}, fmt.Errorf("uploading your SSH key: %w", err)
	}
	var s hzServer
	err = h.do(ctx, "POST", "/servers", map[string]any{
		"name": name, "server_type": r.Plan, "location": r.ID, "image": r.Image,
		"ssh_keys": []int64{kid}, "labels": map[string]string{Tag: "bouncer"},
		"public_net": map[string]bool{"enable_ipv4": true, "enable_ipv6": true},
	}, &s)
	if err != nil {
		return Server{}, err
	}
	id := strconv.FormatInt(s.Server.ID, 10)
	err = poll(ctx, 3*time.Second, func() (bool, error) {
		if err := h.do(ctx, "GET", "/servers/"+id, nil, &s); err != nil {
			return false, err
		}
		return s.Server.Status == "running" && s.Server.PublicNet.IPv4.IP != "", nil
	})
	return Server{ID: id, IPv4: s.Server.PublicNet.IPv4.IP}, err
}

func (h *Hetzner) Delete(ctx context.Context, id string) error {
	err := h.do(ctx, "DELETE", "/servers/"+url.PathEscape(id), nil, nil)
	var ae *APIError
	if errors.As(err, &ae) && ae.Status == 404 {
		return nil // already gone
	}
	return err
}
