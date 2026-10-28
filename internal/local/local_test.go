package local

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MaxRenaud/trout/internal/irc"
)

// TestLocal runs the real soju for this computer (dev/build-soju), so it's
// skipped until that's built.
func TestLocal(t *testing.T) {
	if _, _, err := Find(); err != nil {
		t.Skip(err)
	}
	// A short folder: Unix socket paths can't be long, and t.TempDir's are.
	dir, err := os.MkdirTemp(shortTemp(), "trout-local-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	ctx := context.Background()

	s, err := Start(ctx, dir, "max", "first-pass")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Stop)
	login := func(addr, password string) error {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		c, err := irc.Dial(ctx, irc.Config{Addr: addr, Nick: "max", User: "max", Password: password})
		if err == nil {
			c.Close()
		}
		return err
	}
	if err := login(s.Addr, "first-pass"); err != nil {
		t.Fatal("logging in:", err)
	}
	if err := login(s.Addr, "wrong"); !errors.Is(err, irc.ErrLogin) {
		t.Fatalf("a wrong password: %v", err)
	}

	// Another trout (or one restarted after a crash) finds it running.
	again, err := Start(ctx, dir, "max", "first-pass")
	if err != nil || again.Addr != s.Addr || again.cmd != nil {
		t.Fatalf("second start: %+v %v", again, err)
	}
	again.Stop() // not its soju to stop
	if !s.Running() || login(s.Addr, "first-pass") != nil {
		t.Fatal("the second Stop stopped the first trout's soju")
	}

	// Stopped, then started again: the account is in the database, and
	// the password follows what trout has.
	s.Stop()
	if s.Running() {
		t.Fatal("still running after Stop")
	}
	s2, err := Start(ctx, dir, "max", "second-pass")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s2.Stop)
	if err := login(s2.Addr, "second-pass"); err != nil {
		t.Fatal("after a restart:", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "soju.db")); err != nil {
		t.Fatal(err)
	}
}

// shortTemp is where to make a test's soju folder: /tmp where there is one
// (t.TempDir's paths are too long for a Unix socket), else the system's.
func shortTemp() string {
	if st, err := os.Stat("/tmp"); err == nil && st.IsDir() {
		return "/tmp"
	}
	return os.TempDir()
}
