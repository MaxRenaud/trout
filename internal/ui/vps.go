package ui

import (
	"bufio"
	"cmp"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MaxRenaud/trout/internal/cloud"
	"github.com/MaxRenaud/trout/internal/config"
	"github.com/MaxRenaud/trout/internal/install"
	"github.com/MaxRenaud/trout/internal/state"
)

// A new server at a cloud provider for the bouncer, in the setup wizard:
// the API token (kept in the credentials file), a region with the
// provider's cheapest plan and price, then Create Server, which makes it
// with your SSH key and hands it to the same install as your own server,
// as root. The server stays yours to delete (Bouncers > Remove).

// whereOptions are the wizard's first choice.
var whereOptions = []string{"My own server (over SSH)", "Hetzner Cloud", "DigitalOcean", "Google Cloud"}

var whereNotes = []string{
	"A Linux server you can already SSH into: trout installs soju there.",
	"A new server at Hetzner, billed by the hour by Hetzner. From about $8/month.",
	"A new droplet at DigitalOcean, billed by DigitalOcean. From $4/month.",
	"The free tier's e2-micro, through your gcloud login and project; Google bills about $3.65/month for its IPv4.",
}

// whereIDs are whereOptions' providers.
var whereIDs = []string{"", "hetzner", "digitalocean", "gcloud"}

func (a *App) provider(id, token string) cloud.Provider {
	if a.hooks != nil && a.hooks.cloud != nil {
		return a.hooks.cloud(id, token)
	}
	switch id {
	case "hetzner":
		return cloud.NewHetzner(token)
	case "gcloud":
		return cloud.NewGCloud(token) // the "token" is the project, gcloud's own if empty
	}
	return cloud.NewDigitalOcean(token)
}

func (a *App) cloudToken(id string) string {
	if a.cfg == nil {
		return a.cloudTokens[id]
	}
	tok, _ := a.cfg.StoredPassword("(" + id + ")")
	return tok
}

func (a *App) saveCloudToken(id, token string) {
	if a.cfg == nil {
		if a.cloudTokens == nil {
			a.cloudTokens = map[string]string{}
		}
		a.cloudTokens[id] = token
		return
	}
	if err := a.cfg.SetPassword("("+id+")", token); err != nil {
		a.addLine(a.home.Status(), state.RoleQuit, "* Couldn't save the API token: "+err.Error())
	}
}

// sshPublicKey is the key a new server lets root in with: the ssh agent's
// first, or ~/.ssh's usual ones.
func (a *App) sshPublicKey() (string, error) {
	if a.hooks != nil && a.hooks.sshKey != "" {
		return a.hooks.sshKey, nil
	}
	if out, err := exec.Command("ssh-add", "-L").Output(); err == nil {
		sc := bufio.NewScanner(strings.NewReader(string(out)))
		for sc.Scan() {
			if strings.HasPrefix(sc.Text(), "ssh-") || strings.HasPrefix(sc.Text(), "ecdsa-") {
				return sc.Text(), nil
			}
		}
	}
	home, _ := os.UserHomeDir()
	for _, f := range []string{"id_ed25519.pub", "id_ecdsa.pub", "id_rsa.pub"} {
		if data, err := os.ReadFile(filepath.Join(home, ".ssh", f)); err == nil {
			return strings.TrimSpace(string(data)), nil
		}
	}
	return "", errors.New("you have no SSH key for the new server to let you in with: " +
		"make one with ssh-keygen -t ed25519, then try again")
}

// cloudRemote is ssh to a server trout just created: as root, trusting
// its new host key the first time.
func (a *App) cloudRemote(host string) remote {
	if a.hooks != nil && a.hooks.remote != nil {
		return a.hooks.remote(host)
	}
	return install.SSH{Host: host, Options: []string{"-o", "StrictHostKeyChecking=accept-new"}}
}

// chooseCloud goes on with the provider picked: its token, then regions.
func (w *installWizard) chooseCloud(a *App) tea.Cmd {
	id := whereIDs[w.where]
	w.provID = id
	if tok := a.cloudToken(id); tok != "" || id == "gcloud" { // gcloud has its own login
		return w.loadRegions(a, tok)
	}
	return w.askToken(a, "")
}

func (w *installWizard) askToken(a *App, note string) tea.Cmd {
	name := whereOptions[w.where]
	lines := []string{"trout needs a " + name + " API token to make the server.", ""}
	if w.provID == "hetzner" {
		lines = append(lines, "Make one in the Hetzner Cloud Console: your project >",
			"Security > API tokens > Generate API token, Read & Write.")
	} else {
		lines = append(lines, "Make one at cloud.digitalocean.com > API > Generate New Token,",
			"with read and write for droplets and SSH keys (or full access).", "",
			"No DigitalOcean account yet? Signing up with trout's referral link,",
			cloud.DigitalOceanReferral+", gives you starter credit,",
			"and trout's author credit once you spend some. (Not cash.)")
	}
	lines = append(lines, "", "It's saved in trout's credentials file, next to config.toml.")
	a.openDialog(&formBox{Title: name + " API Token", Lines: lines, Note: note,
		Fields:  []*formField{newField("Token:", "", true)},
		Buttons: []string{"OK", "Cancel"},
		OnOK: func(a *App, vals []string) (tea.Cmd, error) {
			tok := strings.TrimSpace(vals[0])
			if tok == "" {
				return nil, errors.New("paste the token")
			}
			a.openDialog(w)
			return w.loadRegions(a, tok), nil
		},
		OnCancel: func(a *App) tea.Cmd { a.openDialog(w); w.step = instWhere; return nil },
	})
	return nil
}

func (w *installWizard) loadRegions(a *App, token string) tea.Cmd {
	w.seq++
	w.ctx, w.cancel = context.WithCancel(context.Background())
	a.installing = w
	w.token = token
	w.prov = a.provider(w.provID, token)
	w.step, w.log, w.err = instWorking, nil, ""
	w.say("Asking " + w.prov.Name() + " for its regions and prices...")
	p, seq, ctx := w.prov, w.seq, w.ctx
	return func() tea.Msg {
		rs, err := p.Regions(ctx)
		return instPhaseMsg{w: w, seq: seq, phase: "regions", regions: rs, err: err}
	}
}

// create makes the server in the region picked.
func (w *installWizard) create(a *App) tea.Cmd {
	key, err := a.sshPublicKey()
	if err != nil {
		return w.fail(err.Error())
	}
	r := w.regions[w.region]
	w.seq++
	w.ctx, w.cancel = context.WithCancel(context.Background())
	w.step, w.log, w.err = instWorking, nil, ""
	b := make([]byte, 2)
	rand.Read(b)
	w.serverName = fmt.Sprintf("trout-%s-%x", r.ID, b)
	w.say(fmt.Sprintf("Creating %s at %s in %s (%s, %s)...", w.serverName, w.prov.Name(), r.Label, r.Plan, r.Price()))
	p, seq, ctx, name := w.prov, w.seq, w.ctx, w.serverName
	return func() tea.Msg {
		s, err := p.Create(ctx, r, name, key)
		return instPhaseMsg{w: w, seq: seq, phase: "create", server: s, err: err}
	}
}

// cloudPhase handles the cloud steps' results; ok is false for others.
func (w *installWizard) cloudPhase(a *App, m instPhaseMsg) (tea.Cmd, bool) {
	switch m.phase {
	case "regions":
		switch {
		case errors.Is(m.err, cloud.ErrToken):
			return w.askToken(a, w.prov.Name()+" didn't take that token."), true
		case m.err != nil:
			return w.fail(m.err.Error()), true
		case len(m.regions) == 0:
			return w.fail(w.prov.Name() + " has nowhere to make a server just now."), true
		}
		if w.provID != "gcloud" {
			a.saveCloudToken(w.provID, w.token)
		}
		w.regions, w.region, w.step = m.regions, 0, instRegion
		return nil, true
	case "create":
		if m.server.ID != "" {
			w.server = &m.server
		}
		if m.err != nil {
			return w.fail(m.err.Error()), true
		}
		w.say("The server is up at " + m.server.IPv4)
		if a.hooks == nil { // a recycled IP's old host key isn't this server's
			exec.Command("ssh-keygen", "-R", m.server.IPv4).Run()
		}
		w.typed = cmp.Or(m.server.User, "root") + "@" + m.server.IPv4
		return w.start(a), true
	case "deleted":
		if m.err != nil {
			return w.fail("Couldn't delete the server: " + m.err.Error() + ". Delete it in " + w.prov.Name() + "'s console."), true
		}
		a.addLine(a.home.Status(), state.RoleInfo, "* Deleted "+w.serverName+" at "+w.prov.Name())
		w.server = nil
		w.close(a)
		return nil, true
	}
	return nil, false
}

func (w *installWizard) deleteServer() tea.Cmd {
	w.say("Deleting " + w.serverName + "...")
	w.step = instWorking
	p, id, seq := w.prov, w.server.ID, w.seq
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		return instPhaseMsg{w: w, seq: seq, phase: "deleted", err: p.Delete(ctx, id)}
	}
}

// keepOrDelete asks, when the setup is stopped with a server made, whether
// it goes too.
func (w *installWizard) keepOrDelete(a *App) {
	d := message("The new server, "+w.serverName+" ("+w.server.IPv4+"), is still", "running at "+w.prov.Name()+", and billed until it's deleted.")
	d.Title = "Stop"
	d.Buttons, d.Cancel = []string{"Delete Server", "Keep It", "Back"}, 2
	d.OnClose = func(a *App, button int) tea.Cmd {
		switch button {
		case 0:
			a.openDialog(w)
			return w.deleteServer()
		case 1:
			a.addLine(a.home.Status(), state.RoleInfo, "* Kept "+w.serverName+" at "+w.prov.Name()+" ("+w.server.IPv4+")")
			w.close(a)
		default:
			a.openDialog(w)
		}
		return nil
	}
	a.openDialog(d)
}

func (w *installWizard) drawRegions(a *App, c *Canvas, x, y, wd, h int, text func(string, Style)) {
	t := a.theme()
	face := Style{FG: t.FaceText, BG: t.Face}
	bold := Style{FG: t.FaceText, BG: t.Face, Bold: true}
	text("Where? (each region's cheapest "+w.prov.Name()+" plan)", bold)
	rows := h - 9
	w.regionTop = clamp(w.regionTop, w.region-rows+1, w.region)
	w.regionTop = clamp(w.regionTop, 0, max(0, len(w.regions)-rows))
	tx, tw := x+3, wd-6
	row := y + 3
	for i := w.regionTop; i < min(len(w.regions), w.regionTop+rows); i++ {
		r := w.regions[i]
		box := "( ) "
		if i == w.region {
			box = "(•) "
		}
		line := fmt.Sprintf("%s%-22s %-14s %s", box, r.Label, r.Price(), r.PlanDetail)
		c.Text(tx, row, tw, line, face)
		a.addRegion(Region{X: tx - 1, Y: row, W: tw + 2, H: 1, Kind: HitDlg, S: "region", I: i})
		row++
	}
	c.Text(tx, y+h-5, tw, "Create Server starts the billing, until you delete the server.", face)
}

// finishCloud is the bouncer a finished cloud setup saves.
func (w *installWizard) cloudBouncer(b config.Bouncer) config.Bouncer {
	if w.server != nil {
		b.Name, b.Cloud, b.ServerID = w.serverName, w.provID, w.server.ID
	}
	return b
}

// deleteBouncerServer is Bouncers > Remove > Delete Server for a bouncer
// on a server trout created.
func (a *App) deleteBouncerServer(b config.Bouncer, back modal) tea.Cmd {
	tok := a.cloudToken(b.Cloud)
	p := a.provider(b.Cloud, tok)
	a.addLine(a.home.Status(), state.RoleInfo, "* Deleting "+b.Name+" at "+p.Name()+"...")
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		return serverDeletedMsg{b, p.Name(), p.Delete(ctx, b.ServerID)}
	}
}

type serverDeletedMsg struct {
	b    config.Bouncer
	name string
	err  error
}

func (a *App) serverDeleted(m serverDeletedMsg) {
	if m.err != nil {
		a.addLine(a.home.Status(), state.RoleQuit, "* Couldn't delete "+m.b.Name+" at "+m.name+": "+m.err.Error())
		return
	}
	a.addLine(a.home.Status(), state.RoleInfo, "* Deleted "+m.b.Name+" at "+m.name+"; billing for it stops")
	if a.cfg != nil {
		if err := a.cfg.Remove(m.b.Name); err != nil {
			a.addLine(a.home.Status(), state.RoleQuit, "* Couldn't forget "+m.b.Name+": "+err.Error())
		}
	}
}
