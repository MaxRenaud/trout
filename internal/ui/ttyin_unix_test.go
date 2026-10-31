//go:build unix

package ui

import (
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
	go func() {
		for _, chunk := range []string{"\x1b[<35;10;5M", "\x1b", "[<35;111;26M", "\x1b[", "<35;125;35M",
			"\x1b[<35;7", "3;51M\x1b[<35;138;38M", "\x1b", "x"} {
			w.WriteString(chunk)
			time.Sleep(5 * time.Millisecond)
		}
		time.Sleep(2 * escWait)
		w.WriteString("\x1b") // a lone Esc is still Esc, after escWait
		time.Sleep(2 * escWait)
		w.WriteString("\x03")
		w.Close()
	}()
	m := &msgLog{}
	p := tea.NewProgram(m, tea.WithInput(ttyIn{r}), tea.WithOutput(io.Discard), tea.WithMouseAllMotion())
	if _, err := p.Run(); err != nil {
		t.Fatal(err)
	}
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

type msgLog struct{ got []string }

func (m *msgLog) Init() tea.Cmd { return nil }
func (m *msgLog) View() string  { return "" }
func (m *msgLog) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		m.got = append(m.got, msg.String())
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
	case tea.MouseMsg:
		m.got = append(m.got, tea.MouseEvent(msg).String())
	}
	return m, nil
}
