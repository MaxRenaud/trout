package irc

import "testing"

func TestRedact(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"AUTHENTICATE PLAIN", "AUTHENTICATE PLAIN"},
		{"AUTHENTICATE SCRAM-SHA-256", "AUTHENTICATE SCRAM-SHA-256"},
		{"AUTHENTICATE +", "AUTHENTICATE +"},
		{"AUTHENTICATE AHRyb3V0AHB3", "AUTHENTICATE <hidden>"},
		{"AUTHENTICATE AAAA=", "AUTHENTICATE <hidden>"},
		{"PASS hunter2", "PASS <hidden>"},
		{"oper max hunter2", "oper max <hidden>"},
		{"PRIVMSG BouncerServ :user update -password hunter2", "PRIVMSG BouncerServ :user update -password <hidden>"},
		{"PRIVMSG BouncerServ :user create -username bob -password hunter2 -admin=false", "PRIVMSG BouncerServ :user create -username bob -password <hidden> -admin=false"},
		{"PRIVMSG BouncerServ :network create -addr ircs://x -name x -pass hunter2", "PRIVMSG BouncerServ :network create -addr ircs://x -name x -pass <hidden>"},
		{"PRIVMSG BouncerServ :sasl set-plain -network x max hunter2", "PRIVMSG BouncerServ :sasl set-plain -network x max <hidden>"},
		{"PRIVMSG BouncerServ :network status", "PRIVMSG BouncerServ :network status"},
		{"@label=3 PRIVMSG nickserv :identify max hunter2", "@label=3 PRIVMSG nickserv :identify <hidden> <hidden>"},
		{"PRIVMSG NickServ :SET PASSWORD hunter2", "PRIVMSG NickServ :SET PASSWORD <hidden>"},
		{"PRIVMSG NickServ :INFO max", "PRIVMSG NickServ :INFO max"},
		{"NICKSERV IDENTIFY hunter2", "NICKSERV IDENTIFY <hidden>"},
		{"ns :identify hunter2", "ns identify <hidden>"},
		{"PRIVMSG #chan :-password hunter2 is a bad password", "PRIVMSG #chan :-password hunter2 is a bad password"},
		{":srv 001 trout :Welcome", ":srv 001 trout :Welcome"},
		{"", ""},
	} {
		if got := Redact(c.in); got != c.want {
			t.Errorf("Redact(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
