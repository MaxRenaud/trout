//go:build unix

package ui

import (
	"fmt"
	"io"
	"os"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func TestPartialEsc(t *testing.T) {
	for in, want := range map[string]bool{
		"": false, "abc": false, "\x1b": true, "a\x1b": true, "\x1b[": true, "\x1bO": true,
		"\x1b[<35;7": true, "\x1b[<35;73;51": true, "\x1b[<35;73;51M": false, "\x1b[A": false,
		"\x1b[<35;1;1M\x1b[<35": true, "\x1bOA": false, "\x1ba": false, "\x1b[200~hi": false,
		// X10 reports, which GNU screen 4 sends (it has no SGR mouse): ESC [ M and three bytes
		"\x1b[M": true, "\x1b[MC": true, "\x1b[MC|": true, "\x1b[MC|E": false, "\x1b[MC|Ex": false,
		"\x1b[MC|E\x1b[MC": true,
	} {
		if got := partialEsc([]byte(in)); got != want {
			t.Errorf("%q: %v, want %v", in, got, want)
		}
	}
}

// TestSplitMouse: mouse reports split across reads, as they come over ssh
// (Kitty on Windows to a Pi), are mouse events, not typed "<35;111;26M".
func TestSplitMouse(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	m := &msgLog{seen: make(chan int, 64)}
	go func() {
		for _, chunk := range []string{"\x1b[<35;10;5M", "\x1b", "[<35;111;26M", "\x1b[", "<35;125;35M",
			"\x1b[<35;7", "3;51M\x1b[<35;138;38M", "\x1b", "x"} {
			w.WriteString(chunk)
			time.Sleep(5 * time.Millisecond)
		}
		m.waitSeen(t, 6)
		w.WriteString("\x1b") // a lone Esc is still Esc, after escWait
		m.waitSeen(t, 7)
		w.WriteString("\x03")
		w.Close()
	}()
	p := tea.NewProgram(m, tea.WithInput(&ttyIn{File: r}), tea.WithOutput(io.Discard), tea.WithMouseAllMotion())
	runLog(t, p)
	want := []string{"motion", "motion", "motion", "motion", "motion", "alt+x", "esc", "ctrl+c"}
	if len(m.got) != len(want) {
		t.Fatalf("got %q, want %q", m.got, want)
	}
	for i := range want {
		if m.got[i] != want[i] {
			t.Fatalf("got %q, want %q", m.got, want)
		}
	}
}

// TestSplitX10Mouse: GNU screen 4 reports the mouse the old X10 way, ESC
// [ M and three bytes (button, x, y, each plus 32), and sends them in
// pieces; split ones are mouse events, not "C|E" typed in the editbox.
func TestSplitX10Mouse(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	m := &msgLog{seen: make(chan int, 64)}
	go func() {
		for _, chunk := range []string{"\x1b[MC|E", "\x1b[M", "C^M", "\x1b[MC", "HS", "\x1b[MCH", "S", "x"} {
			w.WriteString(chunk)
			time.Sleep(5 * time.Millisecond)
		}
		m.waitSeen(t, 5)
		w.WriteString("\x03")
		w.Close()
	}()
	p := tea.NewProgram(m, tea.WithInput(&ttyIn{File: r}), tea.WithOutput(io.Discard), tea.WithMouseAllMotion())
	runLog(t, p)
	want := fmt.Sprint([]string{"motion", "motion", "motion", "motion", "x", "ctrl+c"})
	if got := fmt.Sprint(m.got); got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

// msgLog records the keys and mouse events a program gets, saying how
// many so far on seen (if set), so a test can wait for them.
type msgLog struct {
	got  []string
	seen chan int
}

func (m *msgLog) Init() tea.Cmd { return nil }
func (m *msgLog) View() string  { return "" }
func (m *msgLog) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		m.got = append(m.got, msg.String())
		m.note()
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
	case tea.MouseMsg:
		m.got = append(m.got, tea.MouseEvent(msg).String())
		m.note()
	}
	return m, nil
}

func (m *msgLog) note() {
	if m.seen != nil {
		select {
		case m.seen <- len(m.got):
		default:
		}
	}
}

// waitSeen waits until m has had n messages, so the next bytes aren't
// read together with the last ones (a lone Esc and a Ctrl+C would be
// Alt+Ctrl+C on a busy machine).
func (m *msgLog) waitSeen(t *testing.T, n int) {
	deadline := time.After(10 * time.Second)
	for {
		select {
		case c := <-m.seen:
			if c >= n {
				return
			}
		case <-deadline:
			t.Errorf("waited for %d messages", n)
			return
		}
	}
}

// runLog runs p, killing it if it hasn't quit within 20 seconds: a key it
// never sees mustn't hang the tests for ten minutes.
func runLog(t *testing.T, p *tea.Program) {
	t.Helper()
	stop := time.AfterFunc(20*time.Second, p.Kill)
	defer stop.Stop()
	if _, err := p.Run(); err != nil {
		t.Fatal(err)
	}
}

// TestBurstMouse: mouse motion that piled up past Bubble Tea's 256-byte
// read, as on a Raspberry Pi 1, is all mouse events: a full read that ends
// partway through a report keeps that part for the next one.
func TestBurstMouse(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	var burst []byte
	for i := range 200 {
		burst = fmt.Appendf(burst, "\x1b[<35;%d;%dM", 10+i%100, 5+i%30)
	}
	go func() {
		w.Write(burst)
		w.WriteString("\x03")
		w.Close()
	}()
	m := &msgLog{}
	p := tea.NewProgram(m, tea.WithInput(&ttyIn{File: r}), tea.WithOutput(io.Discard), tea.WithMouseAllMotion())
	runLog(t, p)
	if len(m.got) != 201 || m.got[200] != "ctrl+c" {
		t.Fatalf("got %d messages, want 200 motions and ctrl+c: %q", len(m.got), m.got)
	}
	for i, got := range m.got[:200] {
		if got != "motion" {
			t.Fatalf("message %d is %q, not a mouse motion", i, got)
		}
	}
}
