// Package local runs trout's own soju on this computer, for using trout
// without a bouncer elsewhere: soju runs as a separate process (it's
// AGPL) while trout does, listening on 127.0.0.1 only, with its database,
// config and log in one folder under trout's config.
package local

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/MaxRenaud/trout/internal/install"
)

// Soju is a soju running from Dir.
type Soju struct {
	Dir  string
	Addr string // 127.0.0.1:port, plain text: it never leaves this computer
	cmd  *exec.Cmd
	done chan struct{}
}

// Find locates soju and sojuctl for this computer; tests replace it.
var Find = func() (soju, sojuctl string, err error) { return install.FindFor(runtime.GOOS, runtime.GOARCH) }

var listenRe = regexp.MustCompile(`(?m)^listen irc\+insecure://(127\.0\.0\.1:[0-9]+)$`)

// Start runs soju from dir, made if needed, with account (an admin) set to
// password. A soju already running from dir (left by a trout that
// crashed, or another one open) is used as it is, and Stop leaves it be.
func Start(ctx context.Context, dir, account, password string) (*Soju, error) {
	soju, sojuctl, err := Find()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	s := &Soju{Dir: dir}
	conf := filepath.Join(dir, "config")
	if addr, ok := s.running(conf); ok {
		s.Addr = addr
	} else {
		if s.Addr, err = freePort(); err != nil {
			return nil, err
		}
		if err := s.start(soju, conf); err != nil {
			return nil, err
		}
	}
	if err := setAccount(ctx, sojuctl, dir, account, password); err != nil {
		s.Stop()
		return nil, err
	}
	return s, nil
}

func (s *Soju) admin() string { return filepath.Join(s.Dir, "admin") }

// running finds a soju already serving dir: its admin socket answers.
func (s *Soju) running(conf string) (string, bool) {
	data, err := os.ReadFile(conf)
	if err != nil {
		return "", false
	}
	m := listenRe.FindSubmatch(data)
	if m == nil {
		return "", false
	}
	c, err := net.DialTimeout("unix", s.admin(), time.Second)
	if err != nil {
		return "", false
	}
	c.Close()
	return string(m[1]), true
}

// freePort is a port nothing listens on just now, on 127.0.0.1.
func freePort() (string, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	defer ln.Close()
	return ln.Addr().String(), nil
}

func (s *Soju) start(soju, conf string) error {
	os.Remove(s.admin()) // left by a soju that didn't stop cleanly
	// Paths relative to Dir, where soju runs: a Windows path in a listen
	// URL loses its backslashes and its drive letter reads as a port.
	config := fmt.Sprintf(`# Written by trout each time it starts its own soju, from this folder.
listen irc+insecure://%s
listen unix+admin://admin
db sqlite3 soju.db
hostname localhost
`, s.Addr)
	if err := os.WriteFile(conf, []byte(config), 0o600); err != nil {
		return err
	}
	logf, err := os.OpenFile(filepath.Join(s.Dir, "soju.log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	s.cmd = exec.Command(soju, "-config", conf)
	s.cmd.Dir = s.Dir
	s.cmd.Stdout, s.cmd.Stderr = logf, logf
	detach(s.cmd)
	if err := s.cmd.Start(); err != nil {
		logf.Close()
		return fmt.Errorf("starting soju: %w", err)
	}
	s.done = make(chan struct{})
	go func() {
		s.cmd.Wait()
		logf.Close()
		close(s.done)
	}()
	// Ready once both listeners answer.
	for deadline := time.Now().Add(10 * time.Second); ; {
		select {
		case <-s.done:
			return fmt.Errorf("soju stopped: %s", lastLines(filepath.Join(s.Dir, "soju.log")))
		default:
		}
		a, err1 := net.DialTimeout("unix", s.admin(), 200*time.Millisecond)
		b, err2 := net.DialTimeout("tcp", s.Addr, 200*time.Millisecond)
		if err1 == nil && err2 == nil {
			a.Close()
			b.Close()
			return nil
		}
		if a != nil {
			a.Close()
		}
		if b != nil {
			b.Close()
		}
		if time.Now().After(deadline) {
			s.Stop()
			return errors.New("soju didn't start in 10s: " + lastLines(filepath.Join(s.Dir, "soju.log")))
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// setAccount makes account, or sets its password, so it always matches
// what trout has in its credentials file.
func setAccount(ctx context.Context, sojuctl, dir, account, password string) error {
	ctl := func(args ...string) error {
		cmd := exec.CommandContext(ctx, sojuctl, append([]string{"-config", "config"}, args...)...)
		cmd.Dir = dir // the config's paths are relative to it
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("sojuctl %s: %v: %s", args[0]+" "+args[1], err, strings.TrimSpace(string(out)))
		}
		return nil
	}
	if ctl("user", "status", account) == nil {
		return ctl("user", "update", account, "-password", password, "-admin=true")
	}
	return ctl("user", "create", "-username", account, "-password", password, "-admin=true")
}

// Stop ends the soju Start started (not one it found running), waiting a
// few seconds for it to close its connections and database.
func (s *Soju) Stop() {
	if s == nil || s.cmd == nil || s.cmd.Process == nil {
		return
	}
	interrupt(s.cmd.Process)
	select {
	case <-s.done:
	case <-time.After(5 * time.Second):
		s.cmd.Process.Kill()
		<-s.done
	}
	s.cmd = nil
}

// PID is the soju process Start started, or 0 (one it found running).
func (s *Soju) PID() int {
	if s == nil || s.cmd == nil || s.cmd.Process == nil {
		return 0
	}
	return s.cmd.Process.Pid
}

// Running reports whether the soju Start started is still up.
func (s *Soju) Running() bool {
	if s == nil || s.done == nil {
		return s != nil // one found running isn't watched
	}
	select {
	case <-s.done:
		return false
	default:
		return true
	}
}

func lastLines(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return "no log"
	}
	defer f.Close()
	var lines []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	if len(lines) > 3 {
		lines = lines[len(lines)-3:]
	}
	return strconv.Quote(strings.Join(lines, " / "))
}
