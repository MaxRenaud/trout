// Package config reads and writes trout's config.toml, and the private
// credentials file next to it that holds bouncer passwords.
package config

import (
	"bytes"
	"cmp"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// Config is config.toml: the bouncers you connect through, which one is
// active, and the settings from the View menu and Options dialog.
type Config struct {
	Active   string    `toml:"active,omitempty"` // a Bouncer's Name
	Bouncers []Bouncer `toml:"bouncer"`
	Settings Settings  `toml:"settings"`

	// Per network name: favorite channels to join quickly (Alt+J), nicks
	// or masks to ignore ("mask [flags] [until=unix]"), and nicks to watch
	// for ("[+]nick [note]", + for a WHOIS when they come on), like mIRC's.
	Favorites map[string][]string `toml:"favorites,omitempty"`
	Ignore    map[string][]string `toml:"ignore,omitempty"`
	Notify    map[string][]string `toml:"notify,omitempty"`

	// Perform is mIRC-style commands the bouncer runs when it connects to
	// a network, by network name. soju keeps them as its connect commands
	// but can't list them, so this is trout's copy.
	Perform map[string][]string `toml:"perform,omitempty"`

	// The Address Book, like mIRC's: people, auto-op/voice/protect, nick
	// colors and highlights. Each applies on every network unless it names
	// one.
	Addresses  []Address       `toml:"address,omitempty"`
	Control    []ControlEntry  `toml:"control,omitempty"`
	NickColors []NickColorRule `toml:"nick-color,omitempty"`
	Highlights []HighlightRule `toml:"highlight-rule,omitempty"`

	// Windows are a window's own options from its popup, like mIRC's
	// system menu, by "network/window" in lowercase.
	Windows map[string]WindowOpts `toml:"window,omitempty"`

	// Proxy is how trout reaches the bouncer and DCC senders, if not
	// directly. Its password is in the credentials file.
	Proxy Proxy `toml:"proxy,omitempty"`

	// DCC, like mIRC's DCC options: nicks or masks whose offers are taken
	// without asking, a folder per kind of file, and file types to ignore
	// (or, with DCCIgnoreMode "accept", the only ones to take).
	DCCTrusted []string    `toml:"dcc-trusted,omitempty"`
	DCCFolders []DCCFolder `toml:"dcc-folder,omitempty"`
	DCCIgnore  []string    `toml:"dcc-ignore"`

	Path string `toml:"-"` // where it was loaded from and is saved to
}

// WindowOpts are one window's own options. Empty means the global
// setting: Timestamps is "on" or "off", Beep "on" (on every message),
// Events "status" or "hide" (joins, parts, quits, nicks and modes),
// Nicklist "left", and TrackURLs "off" keeps its links out of the URLs
// List.
type WindowOpts struct {
	Timestamps string `toml:"timestamps,omitempty"`
	Events     string `toml:"events,omitempty"`
	Beep       string `toml:"beep,omitempty"`
	Nicklist   string `toml:"nicklist,omitempty"`
	TrackURLs  string `toml:"track-urls,omitempty"`
}

// Proxy is mIRC's Connect > Proxy: Type is "socks4", "socks5" or "http"
// (none when empty), used for the bouncer and the file host, for DCC, or
// both, except for hosts matching Except.
type Proxy struct {
	Type    string   `toml:"type,omitempty"`
	Host    string   `toml:"host,omitempty"`
	Port    int      `toml:"port,omitempty"`
	User    string   `toml:"user,omitempty"`
	Bouncer bool     `toml:"bouncer,omitempty"`
	DCC     bool     `toml:"dcc,omitempty"`
	Except  []string `toml:"except,omitempty"`
}

// ProxyPassword is the credentials file's key for the proxy's password;
// the parentheses keep it from being a bouncer's name.
const ProxyPassword = "(proxy)"

// DCCFolder saves files matching Mask (like "*.mp3") in Dir, like mIRC's
// DCC Folders.
type DCCFolder struct {
	Mask string `toml:"mask"`
	Dir  string `toml:"dir"`
}

// DefaultDCCIgnore is mIRC's list of file types not to accept: things
// that run or open in a browser.
var DefaultDCCIgnore = []string{"*.exe", "*.com", "*.bat", "*.dll", "*.ini", "*.mrc", "*.vbs",
	"*.js", "*.pif", "*.scr", "*.lnk", "*.pl", "*.shs", "*.htm", "*.html"}

// Address is an Address Book entry about someone.
type Address struct {
	Nick    string `toml:"nick"`
	Name    string `toml:"name,omitempty"`
	Email   string `toml:"email,omitempty"`
	Website string `toml:"website,omitempty"`
	Notes   string `toml:"notes,omitempty"`
}

// ControlEntry is mIRC's Control tab: auto-op, auto-voice and protect
// ("o", "v", "p" in Kinds) for a mask in some channels (all if none).
type ControlEntry struct {
	Mask     string   `toml:"mask"`
	Kinds    string   `toml:"kinds"`
	Channels []string `toml:"channels,omitempty"`
	Network  string   `toml:"network,omitempty"`
}

// NickColorRule colors nicks matching Mask (with * and ?), and having one
// of Modes ("@", "+"; any if empty), in the lines and the nicklist. The
// first matching rule wins.
type NickColorRule struct {
	Mask    string `toml:"mask"`
	Modes   string `toml:"modes,omitempty"`
	Color   int    `toml:"color"`
	Network string `toml:"network,omitempty"`
}

// HighlightRule is mIRC's Highlight tab: lines with one of Words (comma
// separated, * and ? allowed) in the message, or from a matching nick
// (MatchNick), in one of In (channels or nicks; anywhere if empty).
// Color is a palette color, or -1 for the highlight color.
type HighlightRule struct {
	Words     string `toml:"words"`
	In        string `toml:"in,omitempty"`
	MatchNick bool   `toml:"match-nick,omitempty"`
	MatchText bool   `toml:"match-text"`
	Color     int    `toml:"color"`
	Beep      bool   `toml:"beep"`
	Network   string `toml:"network,omitempty"`
}

// Bouncer is one soju to connect to. Its password lives in the
// credentials file under Name, or comes from PasswordCommand.
type Bouncer struct {
	Name    string `toml:"name"`
	Address string `toml:"address"` // ircs://host[:port], or irc+insecure://host:port
	User    string `toml:"user"`

	// PasswordCommand is run by the shell when the credentials file has no
	// password for this bouncer, e.g. "pass show soju"; its first line of
	// output is the password.
	PasswordCommand string `toml:"password-command,omitempty"`

	// Pin is the fingerprint of a TLS key you chose to trust, for a bouncer
	// whose certificate no known authority signed.
	Pin string `toml:"pin,omitempty"`

	// Login is "certificate" once the bouncer has accepted this computer's
	// client certificate (ClientCert): trout logs in with it (SASL
	// EXTERNAL) and keeps the password as a fallback.
	Login string `toml:"login,omitempty"`

	// SSH is the host (as ssh knows it: a ~/.ssh/config Host or user@host)
	// trout set this soju up on, for Update and Uninstall; empty for a
	// bouncer trout didn't install.
	SSH string `toml:"ssh,omitempty"`

	// Local is trout's own soju, run on this computer while trout runs
	// (internal/local); it has no Address, as its port changes each time.
	Local bool `toml:"local,omitempty"`

	// Cloud and ServerID are the provider ("hetzner", "digitalocean") and
	// server trout created for this bouncer, for Remove > Delete Server.
	Cloud    string `toml:"cloud,omitempty"`
	ServerID string `toml:"server-id,omitempty"`
}

// Settings are the View menu and Options dialog choices.
type Settings struct {
	Theme         string `toml:"theme"` // "classic" or "dark"
	Toolbar       bool   `toml:"toolbar"`
	Switchbar     bool   `toml:"switchbar"`
	Treebar       bool   `toml:"treebar"`
	Nicklist      bool   `toml:"nicklist"`
	Timestamps    bool   `toml:"timestamps"`
	ModePrefix    bool   `toml:"mode-prefix"`
	LineMarker    bool   `toml:"line-marker"`
	LineShading   bool   `toml:"line-shading"`
	NickColors    bool   `toml:"nick-colors"`
	TreebarWidth  int    `toml:"treebar-width"`
	NicklistWidth int    `toml:"nicklist-width"`
	Beep          bool   `toml:"beep"` // on highlights and private messages, when not looking
	// Colors are the Colors dialog's choices: palette numbers by kind of
	// text, like "action=6 notice=5"; the scheme's colors otherwise.
	Colors string `toml:"colors,omitempty"`

	// DCC file offers: "ask", "auto" or "ignore", like mIRC's "On Send
	// request"; with ask, still auto-get from nicks you just messaged
	// (XDCC) if DCCAutoAsked. DCCWarn shows the privacy notice first.
	DCCGet       string `toml:"dcc-get"`
	DCCAutoAsked bool   `toml:"dcc-auto-asked"`
	DCCWarn      bool   `toml:"dcc-warn"`
	DownloadDir  string `toml:"download-dir,omitempty"` // ~/Downloads when empty
	// DCCChat is "ask", "auto" or "ignore" for DCC CHAT offers, like mIRC's
	// "On Chat request"; DCCClose closes a Get window when its file is in.
	DCCChat  string `toml:"dcc-chat"`
	DCCClose bool   `toml:"dcc-close"`
	// DCCIgnoreMode is "ignore" (the file types in DCCIgnore), "accept"
	// (only those) or "off"; DCCTimeout is how many seconds a Get or Chat
	// waits to connect or for data before giving up.
	DCCIgnoreMode string `toml:"dcc-ignore-mode"`
	DCCTimeout    int    `toml:"dcc-timeout"`

	// Highlight is the old highlight words, comma separated; Load turns
	// them into Highlights rules.
	Highlight string `toml:"highlight,omitempty"`

	// Options > IRC, like mIRC's: the timestamp format in mIRC's notation
	// ("[HH:nn]"), rejoining when kicked, joining when invited, private
	// notices in the active window (else the status window), and a WHOIS
	// when someone opens a query with you.
	TimestampFormat string `toml:"timestamp-format"`
	RejoinOnKick    bool   `toml:"rejoin-on-kick"`
	JoinOnInvite    bool   `toml:"join-on-invite"`
	NoticesActive   bool   `toml:"notices-in-active"`
	WhoisOnQuery    bool   `toml:"whois-on-query"`

	// Options > Other: ask before pasting several lines, before quitting,
	// the command prefix, what Tab adds after a nick at the start of a
	// line, and how many lines a window keeps.
	ConfirmPaste bool   `toml:"confirm-paste"`
	ConfirmExit  bool   `toml:"confirm-exit"`
	CmdPrefix    string `toml:"command-prefix"`
	TabSuffix    string `toml:"tab-suffix,omitempty"`
	Buffer       int    `toml:"buffer-lines"`

	// Display > switchbar, like mIRC's: sort the buttons, stretch them to
	// fill the bar, and color the active one's button on new lines too.
	SortSwitch bool `toml:"sort-switchbar"`
	FillSwitch bool `toml:"fill-switchbar"`
	HiActive   bool `toml:"highlight-active"`

	// Options > Messages: where private messages go ("windows", "single",
	// "active", like mIRC's query options), user addresses on joins and
	// parts, the nicks when you join, and notify lines in the active
	// window.
	PrivateTo    string `toml:"private-messages"`
	ShowAddress  bool   `toml:"show-addresses"`
	NicksOnJoin  bool   `toml:"nicks-on-join"`
	NotifyActive bool   `toml:"notify-in-active"`

	// Options > Messages, like mIRC's IRC > Messages and Options: strip
	// codes from what others say, keep a channel's window when you part,
	// <nick> before your own lines, CTCP requests in the active window,
	// coming back from away when you talk, and invisible mode (+i).
	StripCodes   bool `toml:"strip-codes"`
	KeepOpen     bool `toml:"keep-open-on-part"`
	PrefixOwn    bool `toml:"prefix-own"`
	CTCPActive   bool `toml:"ctcp-in-active"`
	AwayCancel   bool `toml:"cancel-away-on-talk"`
	Invisible    bool `toml:"invisible"`
	PopFavorites bool `toml:"favorites-on-connect"` // the Favorites dialog when a network connects

	// OptionKey is what you said about a Mac terminal whose Option key
	// isn't Alt: "" (not asked yet), "fix" (iTerm2: use trout's profile)
	// or "never" (don't ask again).
	OptionKey string `toml:"option-key,omitempty"`
}

func DefaultSettings() Settings {
	return Settings{Theme: "classic", Toolbar: true, Switchbar: true, Treebar: true, Nicklist: true,
		Timestamps: true, ModePrefix: true, LineMarker: true, TreebarWidth: 20, NicklistWidth: 18, Beep: true,
		DCCGet: "ask", DCCAutoAsked: true, DCCWarn: true, DCCChat: "ask", DCCIgnoreMode: "ignore", DCCTimeout: 300,
		TimestampFormat: "[HH:nn]", NoticesActive: true, ConfirmPaste: true, PrefixOwn: true, PopFavorites: true,
		CmdPrefix: "/", Buffer: 500, PrivateTo: "windows", ShowAddress: true}
}

// DefaultPath is config.toml in trout's directory under the user config
// directory, e.g. ~/Library/Application Support/trout on macOS.
func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "trout", "config.toml"), nil
}

// Load reads the config at path. A missing file is an empty config with
// default settings.
func Load(path string) (*Config, error) {
	c := &Config{Settings: DefaultSettings(), Path: path}
	md, err := toml.DecodeFile(path, c)
	if errors.Is(err, fs.ErrNotExist) {
		c.DCCIgnore = slices.Clone(DefaultDCCIgnore)
		return c, nil
	}
	if err != nil {
		return nil, err
	}
	if !md.IsDefined("dcc-ignore") { // an empty list is kept: you cleared it
		c.DCCIgnore = slices.Clone(DefaultDCCIgnore)
	}
	// The old comma-separated highlight words become mIRC-style rules.
	if c.Settings.Highlight != "" && len(c.Highlights) == 0 {
		for w := range strings.SplitSeq(c.Settings.Highlight, ",") {
			if w = strings.TrimSpace(w); w != "" {
				c.Highlights = append(c.Highlights, HighlightRule{Words: w, MatchText: true, Color: -1, Beep: true})
			}
		}
	}
	c.Settings.Highlight = ""
	// trout rewrites the file, so a misspelled key would quietly vanish.
	if keys := md.Undecoded(); len(keys) > 0 {
		var names []string
		for _, k := range keys {
			names = append(names, k.String())
		}
		return nil, fmt.Errorf("%s: unknown settings: %s", path, strings.Join(names, ", "))
	}
	return c, nil
}

const configHeader = `# trout's settings. trout rewrites this file when you change options or
# bouncers, so comments you add here won't be kept. Passwords are in the
# credentials file next to it.

`

// Save writes the config back to its Path.
func (c *Config) Save() error {
	var b bytes.Buffer
	b.WriteString(configHeader)
	if err := toml.NewEncoder(&b).Encode(c); err != nil {
		return err
	}
	return writeFile(c.Path, b.Bytes())
}

// Find returns the bouncer called name, or nil.
func (c *Config) Find(name string) *Bouncer {
	if i := slices.IndexFunc(c.Bouncers, func(b Bouncer) bool { return b.Name == name }); i >= 0 {
		return &c.Bouncers[i]
	}
	return nil
}

// ActiveBouncer is the bouncer to connect to: the one Active names, or the
// only one there is.
func (c *Config) ActiveBouncer() (Bouncer, bool) {
	if b := c.Find(c.Active); b != nil {
		return *b, true
	}
	if c.Active == "" && len(c.Bouncers) == 1 {
		return c.Bouncers[0], true
	}
	return Bouncer{}, false
}

// Use adds b, or replaces the bouncer with its name, and makes it active.
func (c *Config) Use(b Bouncer) {
	if old := c.Find(b.Name); old != nil {
		*old = b
	} else {
		c.Bouncers = append(c.Bouncers, b)
	}
	c.Active = b.Name
}

// credentials is the file next to config.toml with a password per bouncer
// name. Like SSH with its keys, trout won't read it if others can.
func (c *Config) credentialsPath() string { return filepath.Join(filepath.Dir(c.Path), "credentials") }

func (c *Config) loadCredentials() (map[string]string, error) {
	path := c.credentialsPath()
	creds := map[string]string{}
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return creds, nil
	}
	if err != nil {
		return nil, err
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("%s can be read by other users; run chmod 600 on it", path)
	}
	if _, err := toml.DecodeFile(path, &creds); err != nil {
		return nil, err
	}
	return creds, nil
}

// Password finds b's password: from the credentials file, else from its
// PasswordCommand. It's empty when there's neither.
func (c *Config) Password(b Bouncer) (string, error) {
	pw, err := c.StoredPassword(b.Name)
	if err != nil || pw != "" || b.PasswordCommand == "" {
		return pw, err
	}
	return RunPasswordCommand(b.PasswordCommand)
}

// StoredPassword is the password in the credentials file for the bouncer
// called name, or "".
func (c *Config) StoredPassword(name string) (string, error) {
	creds, err := c.loadCredentials()
	return creds[name], err
}

// SetPassword stores the password for the bouncer called name.
func (c *Config) SetPassword(name, password string) error {
	return c.editCredentials(func(creds map[string]string) { creds[name] = password })
}

// Remove forgets the bouncer called name and its password.
func (c *Config) Remove(name string) error {
	c.Bouncers = slices.DeleteFunc(c.Bouncers, func(b Bouncer) bool { return b.Name == name })
	if c.Active == name {
		c.Active = ""
	}
	return errors.Join(c.Save(), c.editCredentials(func(creds map[string]string) { delete(creds, name) }))
}

func (c *Config) editCredentials(edit func(map[string]string)) error {
	creds, err := c.loadCredentials()
	if err != nil {
		return err
	}
	edit(creds)
	var b bytes.Buffer
	b.WriteString("# Bouncer passwords for trout, by bouncer name. Keep this file private.\n\n")
	if err := toml.NewEncoder(&b).Encode(creds); err != nil {
		return err
	}
	return writeFile(c.credentialsPath(), b.Bytes())
}

// RunPasswordCommand runs cmd with the shell, letting it use the terminal
// (for a GPG pinentry, say), and returns the first line it prints.
func RunPasswordCommand(cmd string) (string, error) {
	sh, out := PasswordCommand(cmd)
	sh.Stdin, sh.Stderr = os.Stdin, os.Stderr
	if err := sh.Run(); err != nil {
		return "", fmt.Errorf("password-command %q: %w", cmd, err)
	}
	return FirstLine(out.String()), nil
}

// PasswordCommand is cmd run by the shell, printing into out.
func PasswordCommand(cmd string) (sh *exec.Cmd, out *bytes.Buffer) {
	sh = exec.Command("/bin/sh", "-c", cmd)
	if runtime.GOOS == "windows" {
		sh = exec.Command("cmd", "/C", cmd)
	}
	out = &bytes.Buffer{}
	sh.Stdout = out
	return sh, out
}

// FirstLine is a password-command's password: its first line of output.
func FirstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return strings.TrimSuffix(line, "\r")
}

// writeFile replaces path atomically with a private file, so a crash
// can't leave half a config behind.
func writeFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".trout-*") // created 0600
	if err != nil {
		return err
	}
	defer os.Remove(f.Name()) // fails harmlessly after the rename
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

// ClientCert is this computer's TLS client certificate for logging in to
// bouncers, kept as client.pem (certificate and key) next to config.toml
// and made the first time it's needed.
func (c *Config) ClientCert() (tls.Certificate, error) {
	path := filepath.Join(filepath.Dir(c.Path), "client.pem")
	if data, err := os.ReadFile(path); err == nil {
		return tls.X509KeyPair(data, data)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	host, _ := os.Hostname()
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: "trout on " + cmp.Or(host, "this computer")},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(20, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return tls.Certificate{}, err
	}
	data := slices.Concat(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))
	if err := writeFile(path, data); err != nil {
		return tls.Certificate{}, err
	}
	return tls.X509KeyPair(data, data)
}
