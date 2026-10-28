package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"rsc.io/qr"

	"github.com/MaxRenaud/trout/internal/config"
)

// TestPhoneQR draws Connect a Phone and reads the code back off the
// canvas: every module, from the half blocks' colors, must be the
// encoder's, with a white margin, whatever the theme.
func TestPhoneQR(t *testing.T) {
	a := New(Options{Bouncer: config.Bouncer{Address: "ircs://203.0.113.40:6697", User: "max", Pin: "SHA256:x"},
		Password: "abcdefghijkmnpqrstuvwxyz"})
	a.Update(tea.WindowSizeMsg{Width: 110, Height: 30})
	if !a.phoneOK() {
		t.Fatal("a TLS bouncer isn't phone-ready")
	}
	d := &phoneBox{withPassword: true, sha1: "0123456789abcdef0123456789abcdef01234567"}
	if got := d.link(a); got != "ircs://max:abcdefghijkmnpqrstuvwxyz@203.0.113.40:6697" {
		t.Fatalf("link %q", got)
	}
	code, err := qr.Encode(d.link(a), qr.L)
	if err != nil {
		t.Fatal(err)
	}
	w, h := d.size(a)
	c := NewCanvas(w, h, Style{})
	d.draw(a, c, 0, 0, w, h)
	side := code.Size + 2*quiet
	for my := range side {
		for mx := range side {
			cl := c.cells[(2+my/2)*c.W+2+mx]
			col := cl.St.FG
			if my%2 == 1 {
				col = cl.St.BG
			}
			want := RGB(0xffffff)
			if x, y := mx-quiet, my-quiet; x >= 0 && y >= 0 && x < code.Size && y < code.Size && code.Black(x, y) {
				want = 0x000000
			}
			if cl.R != '▀' || col != want {
				t.Fatalf("module %d,%d: %q %06x, want %06x", mx, my, cl.R, col, want)
			}
		}
	}

	d.withPassword = false
	if got := d.link(a); got != "ircs://max@203.0.113.40:6697" {
		t.Fatalf("without the password: %q", got)
	}
	a.bnc = config.Bouncer{Name: localName, Local: true, User: "max"}
	if a.phoneOK() {
		t.Fatal("This Computer offered to a phone")
	}
	a.bnc = config.Bouncer{Address: "irc+insecure://192.0.2.1:6667", User: "max"}
	if a.phoneOK() {
		t.Fatal("a plain-text bouncer offered to a phone")
	}
}
