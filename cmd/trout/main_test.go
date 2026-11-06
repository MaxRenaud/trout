package main

import (
	"path/filepath"
	"testing"

	"github.com/MaxRenaud/trout/internal/config"
)

func TestParseFlags(t *testing.T) {
	env := map[string]string{"TROUT_CONFIG": "/env/config.toml", "TROUT_ADDR": "ircs://env.example",
		"TROUT_USER": "envuser", "TROUT_PASSWORD": "envpw"}
	getenv := func(k string) string { return env[k] }

	f, err := parseFlags(nil, getenv, "/default/config.toml")
	if err != nil {
		t.Fatal(err)
	}
	if f.path != "/env/config.toml" || f.addr != "ircs://env.example" || f.user != "envuser" || f.password != "envpw" {
		t.Errorf("from the environment: %+v", f)
	}
	f, err = parseFlags([]string{"--config", "/flag.toml", "--addr", "ircs://flag.example", "--user", "u", "--password", "p",
		"--bouncer", "home", "--demo"}, getenv, "/default/config.toml")
	if err != nil {
		t.Fatal(err)
	}
	if f.path != "/flag.toml" || f.addr != "ircs://flag.example" || f.user != "u" || f.password != "p" || f.name != "home" || !f.demo {
		t.Errorf("flags beat the environment: %+v", f)
	}
	if f, _ := parseFlags(nil, func(string) string { return "" }, "/default/config.toml"); f.path != "/default/config.toml" {
		t.Errorf("no flag or environment: config %q", f.path)
	}
	if _, err := parseFlags([]string{"--nope"}, getenv, ""); err == nil {
		t.Error("an unknown flag was taken")
	}
}

func TestPickBouncer(t *testing.T) {
	cfg, err := config.Load(filepath.Join(t.TempDir(), "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.Bouncers = []config.Bouncer{
		{Name: "home", Address: "ircs://home.example", User: "max", Pin: "pin1"},
		{Name: "work", Address: "ircs://work.example:7000", User: "renaud"},
	}
	cfg.Active = "work"
	if err := cfg.SetPassword("home", "homepw"); err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		what                       string
		name, addr, user, password string
		want                       config.Bouncer
		wantPassword               string
	}{
		{what: "the active one", want: cfg.Bouncers[1]},
		{what: "--bouncer", name: "home", want: cfg.Bouncers[0], wantPassword: "homepw"},
		{what: "--bouncer with --addr", name: "home", addr: "ircs://new.example",
			want: config.Bouncer{Name: "home", Address: "ircs://new.example", User: "max", Pin: "pin1"}, wantPassword: "homepw"},
		{what: "--bouncer with --user and --password", name: "home", user: "other", password: "pw",
			want: config.Bouncer{Name: "home", Address: "ircs://home.example", User: "other", Pin: "pin1"}, wantPassword: "pw"},
		{what: "--addr of a saved bouncer keeps its pin and password", addr: "ircs://home.example:6697",
			want: config.Bouncer{Name: "home", Address: "ircs://home.example:6697", User: "max", Pin: "pin1"}, wantPassword: "homepw"},
		{what: "--addr of a new one", addr: "ircs://else.example", user: "x",
			want: config.Bouncer{Address: "ircs://else.example", User: "x"}},
	} {
		b, pw, err := pickBouncer(cfg, c.name, c.addr, c.user, c.password)
		if err != nil {
			t.Errorf("%s: %v", c.what, err)
			continue
		}
		if b != c.want || pw != c.wantPassword {
			t.Errorf("%s: got %+v, %q; want %+v, %q", c.what, b, pw, c.want, c.wantPassword)
		}
	}
	if _, _, err := pickBouncer(cfg, "nope", "", "", ""); err == nil {
		t.Error("an unknown --bouncer was taken")
	}
	if _, _, err := pickBouncer(cfg, "", "gopher://x", "", ""); err == nil {
		t.Error("a bad --addr was taken")
	}
}
