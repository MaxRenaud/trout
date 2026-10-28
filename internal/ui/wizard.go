package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// setupWizard mocks the one-click bouncer setup: pick where soju should
// run, pick a host or region, then watch a fake install that ends with
// your networks moved onto the bouncer. Nothing connects anywhere.
type setupWizard struct {
	step   int // 0 where, 1 host or region, 2 installing, 3 done
	where  int // index into bouncerTargets
	choice int // host or region
	hosts  []string
	script []string
	log    []string // script lines shown so far
	seq    int
}

const (
	stepWhere = iota
	stepTarget
	stepInstall
	stepDone
)

var (
	// The mock-up follows the real wizard (install.go, vps.go): the same
	// choices, notes and steps, made-up servers.
	bouncerTargets = whereOptions
	targetNotes    = whereNotes
	regions        = map[int][]string{
		1: {"Falkenstein, DE      $7.59/month   2 CPUs, 4 GB, arm64", "Nuremberg, DE        $7.59/month   2 CPUs, 4 GB, arm64",
			"Helsinki, FI         $7.59/month   2 CPUs, 4 GB, arm64", "Ashburn, VA, US      $21.09/month  2 CPUs, 2 GB, amd64"},
		2: {"Amsterdam 3          $4.00/month   1 CPU, 512 MB, amd64", "Frankfurt 1          $4.00/month   1 CPU, 512 MB, amd64",
			"London 1             $4.00/month   1 CPU, 512 MB, amd64", "Toronto 1            $4.00/month   1 CPU, 512 MB, amd64"},
		3: {"Oregon, US (us-west1-a)       $3.65/month   the free tier",
			"Iowa, US (us-central1-a)      $3.65/month   the free tier",
			"South Carolina, US (us-east1-b) $3.65/month the free tier"},
	}
)

type wizardTickMsg struct{ seq int }

func wizardTick(seq int) tea.Cmd {
	return tea.Tick(450*time.Millisecond, func(time.Time) tea.Msg { return wizardTickMsg{seq} })
}

func (a *App) openSetup() { a.openDialog(&setupWizard{hosts: sshHosts()}) }

// sshHosts reads the concrete Host entries from ~/.ssh/config, falling back
// to sample hosts so the mock always has something to show.
func sshHosts() []string {
	var hosts []string
	if home, err := os.UserHomeDir(); err == nil {
		if data, err := os.ReadFile(filepath.Join(home, ".ssh", "config")); err == nil {
			for line := range strings.Lines(string(data)) {
				f := strings.Fields(line)
				if len(f) < 2 || !strings.EqualFold(f[0], "Host") {
					continue
				}
				for _, h := range f[1:] {
					if !strings.ContainsAny(h, "*?!") {
						hosts = append(hosts, h)
					}
				}
			}
		}
	}
	if len(hosts) == 0 {
		hosts = []string{"homelab", "vps.example.net", "raspberrypi.local"}
	}
	return hosts
}

func (s *setupWizard) options() []string {
	switch {
	case s.step == stepWhere:
		return bouncerTargets
	case s.step == stepTarget && s.where == 0:
		return s.hosts[:min(len(s.hosts), 7)]
	case s.step == stepTarget:
		return regions[s.where]
	}
	return nil
}

func (s *setupWizard) target() string {
	if s.where == 0 {
		return s.hosts[s.choice]
	}
	region, _, _ := strings.Cut(regions[s.where][s.choice], "  ") // without the price columns
	return region
}

func (s *setupWizard) buttons() []string {
	switch s.step {
	case stepWhere:
		return []string{"Next", "Cancel"}
	case stepTarget:
		if s.where != 0 {
			return []string{"Create Server", "Back", "Cancel"}
		}
		return []string{"Install", "Back", "Cancel"}
	case stepInstall:
		return []string{"Cancel"}
	}
	return []string{"Finish"}
}

func (s *setupWizard) install(a *App) []string {
	var nets []string
	for _, n := range a.nets {
		nets = append(nets, n.Name)
	}
	moved := fmt.Sprintf("Moving %d networks onto the bouncer: %s", len(nets), strings.Join(nets, ", "))
	installed := []string{
		"Uploading soju v0.11.1 (15 MB)",
		"Uploading the TLS key and the setup script",
		"Creating the soju system user",
		"Installing soju v0.11.1",
		"Writing /etc/soju/config",
		"Starting soju",
		"Setting up your account, max",
		"Logging in to check that it works...",
		"Your bouncer is ready. Its TLS key is pinned: SHA256:9f2cQm1yZ3xS0k5mB2vLr8u7TqWnE4h",
		moved,
	}
	if s.where == 0 {
		host := s.target()
		return append([]string{
			"Connecting to " + host + " with ssh...",
			"Found Debian GNU/Linux 13 (trixie), Linux x86_64",
			"sudo works without a password",
		}, installed...)
	}
	region := s.target()
	user := "root"
	if s.where == 3 {
		user = "trout"
	}
	return append([]string{
		"Creating trout-bouncer at " + bouncerTargets[s.where] + " in " + region + "...",
		"The server is up at 203.0.113.40",
		"Connecting to " + user + "@203.0.113.40 with ssh...",
		"Found Debian GNU/Linux 13 (trixie), Linux " + map[bool]string{true: "aarch64", false: "x86_64"}[s.where == 1],
		map[bool]string{true: "sudo works without a password", false: "Logged in as root"}[s.where == 3],
	}, installed...)
}

func (s *setupWizard) title() string { return "Set Up a Bouncer" }

func (s *setupWizard) size(a *App) (int, int) { return 68, 20 }

func (s *setupWizard) draw(a *App, c *Canvas, x, y, w, h int) {
	t := a.theme()
	face := Style{FG: t.FaceText, BG: t.Face}
	bold := Style{FG: t.FaceText, BG: t.Face, Bold: true}
	tx, tw, row := x+3, w-6, y+2
	text := func(line string, st Style) {
		c.Text(tx, row, tw, line, st)
		row++
	}
	sel := s.choice
	if s.step == stepWhere {
		sel = s.where
	}
	radios := func() {
		for i, o := range s.options() {
			box := "( ) "
			if i == sel {
				box = "(•) "
			}
			c.Text(tx, row, tw, box+o, face)
			a.addRegion(Region{X: tx - 1, Y: row, W: tw + 2, H: 1, Kind: HitDlg, S: "opt", I: i})
			row++
		}
	}

	switch s.step {
	case stepWhere:
		text("Where should your bouncer run?", bold)
		row++
		radios()
		row++
		text(targetNotes[s.where], face)
	case stepTarget:
		if s.where == 0 {
			text("Which server? (from ~/.ssh/config)", bold)
			row++
			radios()
		} else {
			text("Signed in to "+bouncerTargets[s.where]+" (mock account).", face)
			row++
			text("Where? (each region's cheapest plan)", bold)
			radios()
			row++
			text("Create Server starts the billing, until you delete the server.", face)
		}
	default:
		head := "Setting up soju on " + s.target() + "..."
		if s.step == stepDone {
			head = "Your bouncer is ready."
		}
		text(head, bold)
		row++
		visible := h - 11
		for _, l := range s.log[max(0, len(s.log)-visible):] {
			text("  "+l, face)
		}
		// A Windows-style progress bar.
		by := y + h - 5
		filled := tw * len(s.log) / max(1, len(s.script))
		c.Fill(tx, by, tw, 1, Style{BG: t.TextBG})
		for i := range filled {
			if i%2 == 0 {
				c.Set(tx+i, by, ' ', Style{BG: t.MenuSel})
			}
		}
	}
	a.dlgButtons(c, x, y+h-3, w, s.buttons(), 0)
}

func (s *setupWizard) press(a *App, button string) tea.Cmd {
	switch button {
	case "Next":
		s.step, s.choice = stepTarget, 0
	case "Back":
		s.step = stepWhere
	case "Install", "Create Server":
		s.step, s.script, s.log = stepInstall, s.install(a), nil
		s.seq++
		return wizardTick(s.seq)
	case "Cancel":
		a.dlg = nil
	case "Finish":
		a.dlg = nil
		if s.where == 0 {
			a.addBouncer(bouncer{Name: s.target(), Where: "your server, over SSH",
				Addr: s.target() + ":6697", Pin: "SHA256:9f2c…e41a"})
		} else {
			a.addBouncer(bouncer{Name: "trout-bouncer", Where: bouncerTargets[s.where] + ", " + s.target(),
				Addr: "203.0.113.40:6697", Pin: "SHA256:4b7d…09c3", VPS: true})
		}
	}
	return nil
}

// advance shows the next line of the fake install.
func (s *setupWizard) advance() tea.Cmd {
	s.log = append(s.log, s.script[len(s.log)])
	if len(s.log) == len(s.script) {
		s.step = stepDone
		return nil
	}
	return wizardTick(s.seq)
}

func (s *setupWizard) key(a *App, k string) tea.Cmd {
	n := len(s.options())
	move := func(d int) {
		if n == 0 {
			return
		}
		if s.step == stepWhere {
			s.where = (s.where + d + n) % n
		} else {
			s.choice = (s.choice + d + n) % n
		}
	}
	switch k {
	case "up":
		move(-1)
	case "down":
		move(1)
	case "enter":
		return s.press(a, s.buttons()[0])
	case "esc":
		return s.press(a, "Cancel")
	}
	return nil
}

func (s *setupWizard) mouse(a *App, m tea.MouseMsg, r Region, double bool) tea.Cmd {
	if m.Action != tea.MouseActionPress || m.Button != tea.MouseButtonLeft {
		return nil
	}
	switch r.S {
	case "opt":
		if s.step == stepWhere {
			s.where = r.I
		} else {
			s.choice = r.I
		}
		if double {
			return s.press(a, s.buttons()[0])
		}
	case "button":
		return s.press(a, s.buttons()[r.I])
	case "close":
		a.dlg = nil
	}
	return nil
}
