// Command bot fills the dev IRC networks with people. Each persona joins
// its channels, chats, comes and goes, sometimes messages you privately,
// and answers when you talk to it.
package main

import (
	"context"
	"flag"
	"log"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/ergochat/irc-go/ircmsg"

	"github.com/MaxRenaud/trout/internal/demo"
	"github.com/MaxRenaud/trout/internal/irc"
)

var personas = []string{"alice", "bob", "carol", "dave", "segfault"}

var replies = []string{
	"hey!", "yeah?", "lol", "same", "I'm a bot, but I like you",
	"have you tried turning it off and on again?", "brb, getting coffee",
}

func main() {
	servers := flag.String("servers", "127.0.0.1:6667=#trout",
		`networks and channels, like "troutnet:6667=#trout,#lobby;devnet:6667=#dev"`)
	me := flag.String("user", "trout", "your nick: the one personas mention, message and answer")
	flag.Parse()
	for spec := range strings.SplitSeq(*servers, ";") {
		addr, chans, _ := strings.Cut(spec, "=")
		for i, nick := range personas {
			go persona(addr, nick, strings.Split(chans, ","), i == 0, *me)
		}
	}
	select {}
}

// persona keeps one fake person connected. The founder joins first, so it
// gets ops on new channels and sets their topics.
func persona(addr, nick string, chans []string, founder bool, me string) {
	if !founder {
		time.Sleep(3 * time.Second)
	}
	for {
		err := run(addr, nick, chans, founder, me)
		log.Printf("%s on %s: %v; reconnecting in 5s", nick, addr, err)
		time.Sleep(5 * time.Second)
	}
}

func run(addr, nick string, chans []string, founder bool, me string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	c, err := irc.Dial(ctx, irc.Config{Addr: addr, Nick: nick, User: nick})
	cancel()
	if err != nil {
		return err
	}
	defer c.Close()
	for _, ch := range chans {
		c.Send("JOIN", ch)
		if founder {
			c.Send("TOPIC", ch, "Welcome to "+ch+" | a local trout dev network | the people here are bots")
		}
	}
	done := make(chan error, 1)
	go func() { done <- c.Run(func(m ircmsg.Message) { answer(c, nick, me, m) }) }()
	for {
		select {
		case err := <-done:
			return err
		case <-time.After(time.Duration(6+rand.IntN(25)) * time.Second):
			act(c, chans, me)
		}
	}
}

// act does one random thing: mostly chatter, sometimes an action, a
// private message to you, or stepping out of a channel for a bit.
func act(c *irc.Conn, chans []string, me string) {
	ch := demo.Pick(chans)
	switch r := rand.IntN(100); {
	case r < 6:
		c.Send("PRIVMSG", me, demo.Pick(demo.QueryChatter))
	case r < 12:
		c.Send("PART", ch, "brb")
		time.AfterFunc(time.Duration(10+rand.IntN(30))*time.Second, func() { c.Send("JOIN", ch) })
	case r < 22:
		act := strings.ReplaceAll(demo.Pick(demo.Actions), "$nick", demo.Pick(append(personas, me)))
		c.Send("PRIVMSG", ch, "\x01ACTION "+act+"\x01")
	default:
		c.Send("PRIVMSG", ch, strings.ReplaceAll(demo.Pick(demo.Chatter), "$me", me))
	}
}

// answer replies when you mention this persona or message it privately.
// Personas only answer you, never each other, so they can't loop.
func answer(c *irc.Conn, nick, me string, m ircmsg.Message) {
	if m.Command != "PRIVMSG" || len(m.Params) < 2 || !strings.EqualFold(m.Nick(), me) {
		return
	}
	target, text := m.Params[0], m.Params[1]
	private := strings.EqualFold(target, nick)
	if !private && !strings.Contains(strings.ToLower(text), nick) {
		return
	}
	to, reply := target, m.Nick()+": "+demo.Pick(replies)
	if private {
		to, reply = m.Nick(), demo.Pick(replies)
	}
	time.AfterFunc(time.Duration(1+rand.IntN(3))*time.Second, func() { c.Send("PRIVMSG", to, reply) })
}
