package ui

import (
	"fmt"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// popularNetwork is a network to pick in Add Network, like mIRC's server
// list.
type popularNetwork struct{ name, host string }

// popularNetworks are netsplit.de's 25 biggest networks (its top 100 on
// 2026-11-02) whose address takes TLS on 6697 with a certificate soju can
// check. mIRC's own list is hundreds of networks, many gone, server by
// server and mostly without TLS; a bouncer wants one address a network.
// Left out from the top for their TLS: Undernet, EFnet, QuakeNet, IRCnet,
// GameSurge, freenode and others (self-signed, or certificates for another
// name, which a pin can't fix behind a round-robin address); /server or
// Other... still adds them.
var popularNetworks = []popularNetwork{
	{"Abjects", "irc.abjects.net"},
	{"AnimeFriends", "irc.animefriends.moe"},
	{"BGirc.com", "irc.bgirc.com"},
	{"Chaat", "irc.chaat.fr"},
	{"Chattersnet", "irc.chattersnet.nl"},
	{"CyberIRC", "irc.cyberirc.org"},
	{"DALnet", "irc.dal.net"},
	{"DigitalIRC", "irc.digitalirc.org"},
	{"EfesNet", "irc.adres.net"},
	{"EsperNet", "irc.esper.net"},
	{"EuropNet", "irc.europnet.org"},
	{"hackint", "irc.hackint.org"},
	{"HybridIRC", "irc.hybridirc.com"},
	{"IRC-Nerds", "irc.irc-nerds.net"},
	{"KampungChat", "irc.kampungchat.org"},
	{"Libera.Chat", "irc.libera.chat"},
	{"Nether.RIP", "irc.nether.rip"},
	{"OFTC", "irc.oftc.net"},
	{"P2P-NET", "irc.p2p-network.net"},
	{"PTirc", "irc.ptirc.org"},
	{"Rizon", "irc.rizon.net"},
	{"SceneP2P", "irc.scenep2p.net"},
	{"SimosNap", "irc.simosnap.com"},
	{"Snoonet", "irc.snoonet.org"},
	{"synIRC", "irc.synirc.net"},
}

// openAddNetwork is Select Server's Add...: pick a network from the list
// (one the bouncer has already is marked), or Other... for any address.
// Either way the network form comes next, filled in, to check or change.
// back is the dialog to return to.
func (a *App) openAddNetwork(back modal) {
	var rows, keys []string
	for _, p := range popularNetworks {
		have := ""
		for _, n := range a.nets[1:] {
			if strings.EqualFold(n.Server, p.host) || strings.EqualFold(n.Name, p.name) {
				have = "  (on the bouncer)"
			}
		}
		rows = append(rows, fmt.Sprintf("%-14s %s%s", p.name, p.host, have))
		keys = append(keys, p.name)
	}
	l := &listBox{Title: "Add Network", Header: "Network        Address", Rows: rows, Keys: keys,
		Buttons: []string{"Add...", "Other...", "Cancel"}}
	l.OnPress = func(a *App, l *listBox, button, key string) tea.Cmd {
		f := a.networkForm(nil, back)
		if button == "Add..." {
			i := slices.IndexFunc(popularNetworks, func(p popularNetwork) bool { return p.name == key })
			if i < 0 {
				return nil
			}
			p := popularNetworks[i]
			f.Fields[0].Edit.Buf, f.Fields[1].Edit.Buf = []rune(p.name), []rune("ircs://"+p.host)
			f.Fields[0].Edit.Pos, f.Fields[1].Edit.Pos = len(p.name), len("ircs://"+p.host)
			f.focus = 2
		}
		a.openDialog(f)
		return nil
	}
	l.OnClose = func(a *App) {
		if back != nil {
			a.openDialog(back)
		}
	}
	a.openDialog(l)
}
