//go:build integration

package ui

import (
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MaxRenaud/trout/internal/state"
)

// TestScriptsLive runs a script in trout (tester) on the dev network:
// tester2 joins a new channel and is welcomed, leaves, and tester3 asks
// !seen, which the script answers from the hash table it kept.
//
//	go test -tags integration ./internal/ui -run TestScriptsLive -v
func TestScriptsLive(t *testing.T) {
	a, pump := liveApp(t)
	var tn *state.Network
	pump("TroutNet", func() bool {
		for _, n := range a.nets[1:] {
			if n.Name == "TroutNet" && a.conns[n] != nil && n.Nick != "" {
				tn = n
			}
		}
		return tn != nil
	})
	a.scriptMem = map[string]string{"seen.mrc": `on *:JOIN:#:if ($nick != $me) msg $chan welcome $nick
on *:PART:#:hadd -m seen $nick $ctime
on $*:TEXT:/^!seen (\S+)/:#:{
  if ($hget(seen,$regml(1))) msg $chan $regml(1) left $duration($calc($ctime - $v1)) ago
  else msg $chan never seen $regml(1)
}`}
	ch := "#scripts" + strconv.FormatInt(time.Now().UnixNano()%1e6, 10)
	if cmd, ok := a.scriptAdmin("load -rs seen.mrc", false); ok {
		a.later(cmd)
	}
	a.ircCommand(tn.Status(), "join "+ch)
	var w *state.Window
	pump("joining "+ch, func() bool {
		w = tn.Find(state.KChannel, ch)
		return w != nil && w.Joined
	})
	said := func(text string) func() bool {
		return func() bool {
			return slices.ContainsFunc(w.Lines, func(l state.Line) bool {
				return l.Role == state.RoleOwn && strings.Contains(l.Text, text)
			})
		}
	}

	p2 := person(t, "tester2")
	p2.send("JOIN " + ch)
	pump("the welcome", said("welcome tester2"))
	p2.send("PART " + ch + " :bye")
	pump("tester2 leaving", func() bool { return w.NickIndex("tester2") < 0 })

	p3 := person(t, "tester3")
	p3.send("JOIN " + ch)
	pump("tester3's welcome", said("welcome tester3"))
	p3.send("PRIVMSG " + ch + " :!seen tester2")
	pump("the !seen answer", said("tester2 left "))
	p3.send("PRIVMSG " + ch + " :!seen nobody")
	pump("the other answer", said("never seen nobody"))
	a.ircCommand(w, "part "+ch)
}
