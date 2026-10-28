package ui

import (
	"cmp"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MaxRenaud/trout/internal/demo"
	"github.com/MaxRenaud/trout/internal/state"
)

// The Channels List (Alt+L, /list), like mIRC's: it shows the last list it
// downloaded for the network, and Get List asks the server again. Servers
// limit how often you can LIST (Rizon answers RPL_TRYAGAIN for a while),
// so the cached list is what you usually want anyway.

// chanList is a network's last downloaded channel list.
type chanList struct {
	rows []demo.ChanInfo
	at   time.Time
}

type listTimeoutMsg struct {
	net *state.Network
	seq int
}

// listWait is how long trout waits for a LIST before giving up; soju
// itself drops a pending LIST after 30 seconds without saying so.
const listWait = 60 * time.Second

// openChannelsList shows the active network's Channels List, asking the
// server for it if there's none yet, or if get.
func (a *App) openChannelsList(get bool) tea.Cmd {
	n := a.active.Net
	join := joinList(func(a *App, ch string) { a.join(n, ch) })
	l := &listBox{Title: "Channels List - " + n.Name,
		Header: fmt.Sprintf("%-18s %5s  %s", "Channel", "Users", "Topic"), OnPress: join}
	if a.opts.Demo {
		l.setChannels(demo.Channels[n.Name])
		a.openDialog(l)
		return nil
	}
	l.Buttons = []string{"Join", "Get List", "Filter...", "Close"}
	l.OnPress = func(a *App, l *listBox, button, key string) tea.Cmd {
		switch button {
		case "Get List":
			return a.getChannelList(n, l)
		case "Filter...":
			a.openListFilter(n, l)
			return nil
		}
		return join(a, l, button, key)
	}
	a.listing[n] = l
	if a.chanLists[n] == nil {
		a.chanLists[n] = a.loadChannelList(n)
	}
	a.showChannelList(n, l, "")
	var cmd tea.Cmd
	if a.chanLists[n] == nil || get {
		cmd = a.getChannelList(n, l)
	}
	a.openDialog(l)
	return cmd
}

// getChannelList sends LIST, unless one is already on its way.
func (a *App) getChannelList(n *state.Network, l *listBox) tea.Cmd {
	if n == a.home {
		l.Rows, l.Keys = []string{"Pick a network first: the bouncer's own window has no channels."}, nil
		return nil
	}
	if a.listWaiting[n] != 0 || !a.sendNet(n, "LIST") {
		return nil
	}
	a.listSeq++
	seq := a.listSeq
	a.listWaiting[n], a.listRows[n] = seq, nil
	a.showChannelList(n, l, "")
	return tea.Tick(listWait, func(time.Time) tea.Msg { return listTimeoutMsg{n, seq} })
}

// showChannelList fills the dialog from the cached list, saying how old
// it is, whether a new one is on its way, and note.
func (a *App) showChannelList(n *state.Network, l *listBox, note string) {
	c := a.chanLists[n]
	l.Title = "Channels List - " + n.Name
	switch {
	case c != nil:
		f := a.listFilter[n]
		rows := slices.DeleteFunc(slices.Clone(c.rows), func(ch demo.ChanInfo) bool { return !f.matches(ch) })
		l.setChannels(rows)
		if len(rows) == 0 {
			l.Rows = []string{"No channels"}
		}
		l.Title += " (got " + ago(c.at) + ")"
		if f != (listFilter{}) {
			l.Title += fmt.Sprintf(", %d of %d", len(rows), len(c.rows))
		}
	case a.listWaiting[n] != 0:
		l.Rows, l.Keys = []string{"Downloading the channel list..."}, nil
	default:
		l.Rows, l.Keys = []string{"No list yet: Get List asks the server."}, nil
	}
	if a.listWaiting[n] != 0 && c != nil {
		note = cmp.Or(note, "downloading a new one...")
	}
	if note != "" {
		l.Title += " - " + note
	}
}

// listReply handles RPL_LIST (322) and RPL_LISTEND (323). A LIST trout
// didn't ask for (typed with filters, say) is shown in the status window.
func (a *App) listReply(n *state.Network, code string, p []string) {
	if a.listWaiting[n] == 0 {
		if code == "322" && len(p) >= 4 {
			a.addLine(n.Status(), state.RoleNormal, fmt.Sprintf("%s %s %s", p[1], p[2], p[3]))
		}
		return
	}
	if code == "322" {
		if len(p) >= 4 {
			users := 0
			fmt.Sscan(p[2], &users)
			a.listRows[n] = append(a.listRows[n], demo.ChanInfo{Name: p[1], Users: users, Topic: p[3]})
		}
		return
	}
	rows := a.listRows[n]
	slices.SortFunc(rows, func(x, y demo.ChanInfo) int { return cmp.Compare(y.Users, x.Users) })
	a.chanLists[n] = &chanList{rows: rows, at: time.Now()}
	a.saveChannelList(n)
	a.listDone(n, "")
}

// listFailed ends a LIST that won't finish: the server said to try again
// later (RPL_TRYAGAIN), or never answered. The cached list stays.
func (a *App) listFailed(n *state.Network, why string) {
	if a.listWaiting[n] != 0 {
		a.listDone(n, why)
	}
}

func (a *App) listDone(n *state.Network, note string) {
	delete(a.listWaiting, n)
	delete(a.listRows, n)
	if l := a.listing[n]; l != nil {
		a.showChannelList(n, l, note)
	}
}

// ago is how long ago t was, roughly.
func ago(t time.Time) string {
	switch d := time.Since(t); {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d h ago", int(d.Hours()))
	default:
		return t.Format("Jan 2")
	}
}

// tryAgainText shortens a server's "try again later" for the title.
func tryAgainText(reason string) string {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return "the server says try again later"
	}
	return "the server says: " + reason
}

// The last list is kept on disk too, next to config.toml, like mIRC kept
// its list in a file, so a new session doesn't start with an empty one.

type savedList struct {
	At   time.Time       `json:"at"`
	Rows []demo.ChanInfo `json:"rows"`
}

func (a *App) channelListPath(n *state.Network) string {
	if a.cfg == nil {
		return ""
	}
	return filepath.Join(filepath.Dir(a.cfg.Path), "cache", safeName(a.bnc.Name+"-"+n.Name)+".json")
}

func (a *App) loadChannelList(n *state.Network) *chanList {
	path := a.channelListPath(n)
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var s savedList
	if json.Unmarshal(data, &s) != nil {
		return nil
	}
	return &chanList{rows: s.Rows, at: s.At}
}

func (a *App) saveChannelList(n *state.Network) {
	path, c := a.channelListPath(n), a.chanLists[n]
	if path == "" || c == nil {
		return
	}
	data, err := json.Marshal(savedList{At: c.at, Rows: c.rows})
	if err == nil {
		err = os.MkdirAll(filepath.Dir(path), 0o700)
	}
	if err == nil {
		err = os.WriteFile(path, data, 0o600)
	}
	if err != nil {
		a.addLine(n.Status(), state.RoleInfo, "* Couldn't keep the channel list: "+err.Error())
	}
}

// listFilter is the Channels List's filter, like mIRC's: text in the name
// or topic, and a minimum number of people.
type listFilter struct {
	text     string
	minUsers int
}

func (f listFilter) matches(ch demo.ChanInfo) bool {
	if ch.Users < f.minUsers {
		return false
	}
	t := strings.ToLower(f.text)
	return t == "" || strings.Contains(strings.ToLower(ch.Name), t) || strings.Contains(strings.ToLower(stripCodes(ch.Topic)), t)
}

// openListFilter asks for the filter, then shows the list again.
func (a *App) openListFilter(n *state.Network, l *listBox) {
	f := a.listFilter[n]
	min := ""
	if f.minUsers > 0 {
		min = strconv.Itoa(f.minUsers)
	}
	a.openDialog(&formBox{Title: "Filter Channels", Buttons: []string{"OK", "Cancel"},
		Lines:  []string{"Show channels whose name or topic has the text,", "with at least that many people. Empty shows all."},
		Fields: []*formField{newField("Text:", f.text, false), newField("Min users:", min, false)},
		OnOK: func(a *App, v []string) (tea.Cmd, error) {
			nf := listFilter{text: strings.TrimSpace(v[0])}
			if m := strings.TrimSpace(v[1]); m != "" {
				var err error
				if nf.minUsers, err = strconv.Atoi(m); err != nil {
					return nil, fmt.Errorf("the minimum is a number of people")
				}
			}
			a.listFilter[n] = nf
			a.showChannelList(n, l, "")
			a.openDialog(l)
			return nil, nil
		},
		OnCancel: func(a *App) tea.Cmd { a.openDialog(l); return nil }})
}
