package config

import "testing"

// A password-command with quotes reaches cmd.exe as typed (Go's own
// quoting would turn them into \", which cmd.exe doesn't read).
func TestShellQuotes(t *testing.T) {
	c := &Config{Path: t.TempDir() + `\config.toml`}
	pw, err := c.Password(Bouncer{Name: "x", PasswordCommand: `echo "hunter 2"`})
	if err != nil || pw != `"hunter 2"` {
		t.Fatalf("Password = %q, %v; want the quotes cmd.exe's echo keeps", pw, err)
	}
	if got := shell(`"C:\Program Files\x.exe" a`).SysProcAttr.CmdLine; got != `cmd.exe /S /C ""C:\Program Files\x.exe" a"` {
		t.Errorf("command line %s", got)
	}
}
