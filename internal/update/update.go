// Package update is trout updating itself: asking GitHub for the latest
// release, working out how this trout was installed, and putting the new
// one in, by replacing the files of a downloaded archive in place or with
// Homebrew. Like mIRC's Check for Updates.
package update

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"cmp"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Repo is where trout's releases are.
const Repo = "MaxRenaud/trout"

// API is GitHub's latest-release endpoint; tests point it elsewhere.
var API = "https://api.github.com/repos/" + Repo + "/releases/latest"

// How is how this trout was installed, which decides how it updates.
type How int

const (
	FromSource How = iota // go run or go build: nothing to compare, no checks
	GoInstall             // go install …@version: it says the command
	Homebrew              // the cask: brew upgrades it
	Archive               // a release archive unpacked somewhere: trout replaces its files
)

// Install is this trout's installation.
type Install struct {
	How      How
	Dir      string // where trout and its soju-* folders are
	Brew     string // Homebrew's brew, for Homebrew
	Writable bool   // Archive: trout can replace the files in Dir
}

// Detect works out how the running trout was installed: release says
// whether it's a release build (goreleaser set its version), and
// moduleVersion is the build info's (set by go install …@version).
func Detect(release bool, moduleVersion string) Install {
	exe, err := os.Executable()
	if err != nil {
		return Install{}
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real // Homebrew links bin/trout into the Caskroom
	}
	in := detect(exe, release, moduleVersion)
	if in.How == Archive {
		in.Writable = writable(in.Dir)
	}
	return in
}

func detect(exe string, release bool, moduleVersion string) Install {
	dir := filepath.Dir(exe)
	switch {
	case release:
		slash := filepath.ToSlash(exe)
		if prefix, _, ok := strings.Cut(slash, "/Caskroom/trout/"); ok {
			return Install{How: Homebrew, Dir: dir, Brew: filepath.FromSlash(prefix + "/bin/brew")}
		}
		return Install{How: Archive, Dir: dir}
	case moduleVersion != "" && moduleVersion != "(devel)":
		return Install{How: GoInstall, Dir: dir}
	}
	return Install{How: FromSource, Dir: dir}
}

// writable reports whether trout can make files in dir.
func writable(dir string) bool {
	f, err := os.CreateTemp(dir, ".trout-write-*")
	if err != nil {
		return false
	}
	f.Close()
	os.Remove(f.Name())
	return true
}

// Release is a release on GitHub.
type Release struct {
	Version string            // its tag, like "v0.4.0"
	URL     string            // its page, with the release notes
	Assets  map[string]string // file name to download URL
	Sizes   map[string]int64  // file name to size
}

// Latest asks GitHub for the latest release (not drafts or pre-releases).
func Latest(ctx context.Context, client *http.Client, userAgent string) (Release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, API, nil)
	if err != nil {
		return Release{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", userAgent)
	resp, err := client.Do(req)
	if err != nil {
		return Release{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Release{}, fmt.Errorf("GitHub said %s", resp.Status)
	}
	var r struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
		Assets  []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
			Size int64  `json:"size"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return Release{}, fmt.Errorf("reading GitHub's answer: %w", err)
	}
	if r.TagName == "" {
		return Release{}, errors.New("GitHub's answer has no version")
	}
	rel := Release{Version: r.TagName, URL: r.HTMLURL, Assets: map[string]string{}, Sizes: map[string]int64{}}
	for _, a := range r.Assets {
		rel.Assets[a.Name], rel.Sizes[a.Name] = a.URL, a.Size
	}
	return rel, nil
}

// Newer reports whether version latest is newer than current. Either may
// have a leading v; a build from source ("dev …") is never compared.
func Newer(latest, current string) bool {
	if strings.HasPrefix(current, "dev") || current == "" {
		return false
	}
	return Compare(latest, current) > 0
}

// Compare compares two versions like v1.2.3 part by part, as numbers:
// a missing or unreadable part counts as 0, and anything after a - or +
// (a pre-release or a snapshot) is ignored.
func Compare(a, b string) int {
	pa, pb := parts(a), parts(b)
	for i := range max(len(pa), len(pb)) {
		var x, y int
		if i < len(pa) {
			x = pa[i]
		}
		if i < len(pb) {
			y = pb[i]
		}
		if c := cmp.Compare(x, y); c != 0 {
			return c
		}
	}
	return 0
}

func parts(v string) []int {
	v = strings.TrimPrefix(v, "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	var out []int
	for p := range strings.SplitSeq(v, ".") {
		n, _ := strconv.Atoi(p)
		out = append(out, n)
	}
	return out
}

// Plain is a version without its v, the way trout shows its own: "0.4.0".
func Plain(v string) string { return strings.TrimPrefix(v, "v") }

// AssetName is the release archive for an OS and architecture, as
// .goreleaser.yaml names them: trout_0.4.0_linux_armv6.tar.gz.
func AssetName(version, goos, goarch string) string {
	if goarch == "arm" {
		goarch = "armv6" // GOARM=6, for every Raspberry Pi
	}
	ext := ".tar.gz"
	if goos == "windows" {
		ext = ".zip"
	}
	return "trout_" + Plain(version) + "_" + goos + "_" + goarch + ext
}

// BrewArgs are the brew commands that update the cask, run one after the
// other: update first, since brew only looks at taps once a day on its own.
func BrewArgs() [][]string {
	return [][]string{{"update", "--quiet"}, {"upgrade", "--cask", "maxrenaud/tap/trout"}}
}

// BrewEnv is the environment for brew: brew mustn't remove or clean up
// anything but trout's old version (autoremove once took an unrelated
// formula), and it has just updated.
func BrewEnv() []string {
	return append(os.Environ(), "HOMEBREW_NO_AUTOREMOVE=1", "HOMEBREW_NO_INSTALL_CLEANUP=1",
		"HOMEBREW_NO_ENV_HINTS=1", "HOMEBREW_NO_AUTO_UPDATE=1")
}

// UpdateBrew runs BrewArgs with brew, saying each one with progress.
func UpdateBrew(ctx context.Context, brew string, progress func(string)) error {
	for _, args := range BrewArgs() {
		progress("brew " + strings.Join(args, " "))
		cmd := exec.CommandContext(ctx, brew, args...)
		cmd.Env = BrewEnv()
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("brew %s: %w: %s", args[0], err, lastLine(out))
		}
	}
	return nil
}

func lastLine(out []byte) string {
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// Apply downloads rel's archive for goos/goarch, checks it against the
// release's checksums.txt, and replaces trout's files in dir with the
// ones in it. Each file it replaces is kept as name.old until Cleanup,
// since Windows can't delete a program that's running, only rename it.
func Apply(ctx context.Context, client *http.Client, rel Release, dir, goos, goarch string, progress func(string)) error {
	name := AssetName(rel.Version, goos, goarch)
	url, ok := rel.Assets[name]
	if !ok {
		return fmt.Errorf("the release has no %s", name)
	}
	sumsURL, ok := rel.Assets["checksums.txt"]
	if !ok {
		return errors.New("the release has no checksums.txt")
	}
	sums, err := fetch(ctx, client, sumsURL)
	if err != nil {
		return fmt.Errorf("getting checksums.txt: %w", err)
	}
	want, ok := checksum(sums, name)
	if !ok {
		return fmt.Errorf("checksums.txt has no %s", name)
	}

	progress(fmt.Sprintf("downloading %s (%d MB)", name, rel.Sizes[name]>>20))
	archive, err := os.CreateTemp(dir, ".trout-update-*")
	if err != nil {
		return err
	}
	defer os.Remove(archive.Name())
	defer archive.Close()
	if err := download(ctx, client, url, archive, want); err != nil {
		return fmt.Errorf("downloading %s: %w", name, err)
	}

	stage, err := os.MkdirTemp(dir, ".trout-update-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	progress("unpacking")
	var files []string
	if strings.HasSuffix(name, ".zip") {
		files, err = unzip(archive, stage)
	} else {
		files, err = untar(archive, stage)
	}
	if err != nil {
		return fmt.Errorf("unpacking %s: %w", name, err)
	}
	trout := "trout"
	if goos == "windows" {
		trout += ".exe"
	}
	if _, err := os.Stat(filepath.Join(stage, trout)); err != nil {
		return fmt.Errorf("%s has no %s", name, trout)
	}
	return swap(stage, dir, files)
}

func fetch(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	resp, err := get(ctx, client, url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
}

func get(ctx context.Context, client *http.Client, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("GitHub said %s", resp.Status)
	}
	return resp, nil
}

// checksum finds name's SHA-256 in a checksums.txt ("<hex>  <name>" lines).
func checksum(sums []byte, name string) (string, bool) {
	sc := bufio.NewScanner(strings.NewReader(string(sums)))
	for sc.Scan() {
		if f := strings.Fields(sc.Text()); len(f) == 2 && f[1] == name {
			return strings.ToLower(f[0]), true
		}
	}
	return "", false
}

// download writes url to f, failing unless its SHA-256 is want.
func download(ctx context.Context, client *http.Client, url string, f *os.File, want string) error {
	resp, err := get(ctx, client, url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(f, h), resp.Body); err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		return fmt.Errorf("its checksum is %s, not %s as checksums.txt says", got, want)
	}
	_, err = f.Seek(0, io.SeekStart)
	return err
}

// untar unpacks a .tar.gz's regular files into dir, returning their
// paths (slash-separated, relative to dir).
func untar(r io.Reader, dir string) ([]string, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	var files []string
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return files, nil
		}
		if err != nil {
			return nil, err
		}
		switch h.Typeflag {
		case tar.TypeDir:
			continue
		case tar.TypeReg:
		default:
			return nil, fmt.Errorf("%s isn't a plain file", h.Name)
		}
		if err := unpack(dir, h.Name, h.FileInfo().Mode(), tr); err != nil {
			return nil, err
		}
		files = append(files, h.Name)
	}
}

// unzip is untar for a .zip.
func unzip(f *os.File, dir string) ([]string, error) {
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	zr, err := zip.NewReader(f, st.Size())
	if err != nil {
		return nil, err
	}
	var files []string
	for _, zf := range zr.File {
		if zf.FileInfo().IsDir() {
			continue
		}
		if !zf.Mode().IsRegular() {
			return nil, fmt.Errorf("%s isn't a plain file", zf.Name)
		}
		rc, err := zf.Open()
		if err != nil {
			return nil, err
		}
		err = unpack(dir, zf.Name, zf.Mode(), rc)
		rc.Close()
		if err != nil {
			return nil, err
		}
		files = append(files, zf.Name)
	}
	return files, nil
}

// unpack writes one file of an archive under dir, refusing a name that
// would land outside it.
func unpack(dir, name string, mode os.FileMode, r io.Reader) error {
	if !filepath.IsLocal(filepath.FromSlash(name)) {
		return fmt.Errorf("%s would land outside trout's folder", name)
	}
	path := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode.Perm()|0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, r); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// swap moves each of files from stage into dir, renaming what's there to
// name.old first; if one fails, the ones done so far are put back.
func swap(stage, dir string, files []string) error {
	type moved struct{ path, old string }
	var done []moved
	undo := func() {
		for _, m := range done {
			os.Remove(m.path)
			if m.old != "" {
				rename(m.old, m.path)
			}
		}
	}
	for _, f := range files {
		path := filepath.Join(dir, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			undo()
			return err
		}
		m := moved{path: path}
		if _, err := os.Lstat(path); err == nil {
			m.old = path + ".old"
			os.Remove(m.old) // left by an update whose trout has quit since
			if err := rename(path, m.old); err != nil {
				undo()
				return fmt.Errorf("moving %s aside: %w", f, err)
			}
		}
		if err := rename(filepath.Join(stage, filepath.FromSlash(f)), path); err != nil {
			if m.old != "" {
				rename(m.old, path)
			}
			undo()
			return fmt.Errorf("putting in %s: %w", f, err)
		}
		done = append(done, m)
	}
	return nil
}

// rename is os.Rename, tried again for up to 2 seconds while Windows says
// another process has the file: Defender scanning a program that just
// started, or the start itself, holds it for a moment. Go's own tools do
// the same (cmd/go's robustio).
func rename(from, to string) error {
	err := os.Rename(from, to)
	for range 20 {
		if err == nil || !busy(err) {
			break
		}
		time.Sleep(100 * time.Millisecond)
		err = os.Rename(from, to)
	}
	return err
}

// busy is Windows' ERROR_ACCESS_DENIED, ERROR_SHARING_VIOLATION or
// ERROR_LOCK_VIOLATION.
func busy(err error) bool {
	var errno syscall.Errno
	return runtime.GOOS == "windows" && errors.As(err, &errno) && (errno == 5 || errno == 32 || errno == 33)
}

// Cleanup removes the .old files an update left in dir, once the trout
// that was running then has quit. Ones still in use stay for next time.
func Cleanup(dir string) {
	for _, pattern := range []string{"*.old", "soju-*/*.old"} {
		olds, _ := filepath.Glob(filepath.Join(dir, pattern))
		for _, old := range olds {
			os.Remove(old)
		}
	}
}
