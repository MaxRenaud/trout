package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestMain lets the test binary stand in for a running trout: with
// TROUT_TEST_SLEEP set it just waits, so TestApplyWhileRunning can
// replace it while it runs.
func TestMain(m *testing.M) {
	if os.Getenv("TROUT_TEST_SLEEP") != "" {
		time.Sleep(time.Minute)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestCompare(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{
		{"v0.11.1", "v0.11.1", 0},
		{"v0.9.0", "v0.10.0", -1},
		{"v0.10.1", "v0.10.0", 1},
		{"v1.0.0", "v0.11.1", 1},
		{"v0.11", "v0.11.0", 0},
		{"0.3.5", "v0.3.5", 0}, // a release build's version has no v; its tag does
		{"v0.4.0", "0.3.5", 1},
		{"0.3.6-SNAPSHOT-abc1234", "0.3.6", 0},
	} {
		if got := Compare(c.a, c.b); got != c.want {
			t.Errorf("Compare(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
	if !Newer("v0.4.0", "0.3.5") || Newer("v0.3.5", "0.3.5") || Newer("v9.0.0", "dev 1234567") || Newer("v9.0.0", "") {
		t.Error("Newer")
	}
}

func TestDetect(t *testing.T) {
	for _, c := range []struct {
		exe     string
		release bool
		module  string
		want    Install
	}{
		{"/opt/homebrew/Caskroom/trout/0.3.5/trout", true, "", Install{How: Homebrew, Dir: "/opt/homebrew/Caskroom/trout/0.3.5", Brew: "/opt/homebrew/bin/brew"}},
		{"/home/linuxbrew/.linuxbrew/Caskroom/trout/0.3.5/trout", true, "", Install{How: Homebrew, Dir: "/home/linuxbrew/.linuxbrew/Caskroom/trout/0.3.5", Brew: "/home/linuxbrew/.linuxbrew/bin/brew"}},
		{"/home/max/trout_0.3.5_linux_amd64/trout", true, "", Install{How: Archive, Dir: "/home/max/trout_0.3.5_linux_amd64"}},
		{"/home/max/go/bin/trout", false, "v0.3.5", Install{How: GoInstall, Dir: "/home/max/go/bin"}},
		{"/tmp/go-build123/exe/trout", false, "(devel)", Install{How: FromSource, Dir: "/tmp/go-build123/exe"}},
	} {
		exe := filepath.FromSlash(c.exe)
		c.want.Dir, c.want.Brew = filepath.FromSlash(c.want.Dir), filepath.FromSlash(c.want.Brew)
		if got := detect(exe, c.release, c.module); got != c.want {
			t.Errorf("detect(%q) = %+v, want %+v", c.exe, got, c.want)
		}
	}
}

// TestCleanupOnlyOurs: with no update ever run, Cleanup leaves a user's
// own .old files next to trout alone, and still removes an old trout's
// trout.old from before trout listed them.
func TestCleanupOnlyOurs(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"trout", "trout.old", "script.sh.old", "soju-linux-amd64/notes.old"} {
		path := filepath.Join(dir, filepath.FromSlash(f))
		os.MkdirAll(filepath.Dir(path), 0o755)
		os.WriteFile(path, []byte(f), 0o644)
	}
	Cleanup(dir)
	for f, want := range map[string]bool{"trout": true, "trout.old": false, "script.sh.old": true, "soju-linux-amd64/notes.old": true} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(f))); (err == nil) != want {
			t.Errorf("%s: there %v, want %v", f, err == nil, want)
		}
	}
}

// TestDetectAt: what self-update would replace, found from the program's
// real path, and whether trout may write there.
func TestDetectAt(t *testing.T) {
	root := t.TempDir()
	mk := func(path string) string {
		path = filepath.Join(root, filepath.FromSlash(path))
		os.MkdirAll(filepath.Dir(path), 0o755)
		if err := os.WriteFile(path, nil, 0o755); err != nil {
			t.Fatal(err)
		}
		return path
	}
	cask := mk("brew/Caskroom/trout/0.5.1/trout")
	archive := mk("trout_0.5.1_linux_amd64/trout")
	link := filepath.Join(root, "brew", "bin", "trout")
	os.MkdirAll(filepath.Dir(link), 0o755)
	if err := os.Symlink(cask, link); err != nil {
		t.Skip("no symlinks here:", err)
	}
	real := func(p string) string { r, _ := filepath.EvalSymlinks(p); return r }

	in := detectAt(link, true, "")
	if in.How != Homebrew || in.Dir != filepath.Dir(real(cask)) || in.Brew != filepath.Join(real(root), "brew", "bin", "brew") || in.Writable {
		t.Errorf("Homebrew's link: %+v", in)
	}
	in = detectAt(archive, true, "")
	if in.How != Archive || in.Dir != filepath.Dir(real(archive)) || !in.Writable {
		t.Errorf("an archive: %+v", in)
	}
	if left, _ := filepath.Glob(filepath.Join(in.Dir, ".trout-*")); len(left) != 0 {
		t.Errorf("checking left %q", left)
	}
	if in := detectAt(archive, false, "v0.5.1"); in.How != GoInstall || in.Writable {
		t.Errorf("go install: %+v", in)
	}
	if runtime.GOOS != "windows" && os.Geteuid() != 0 { // root writes anywhere; Windows ignores the mode
		os.Chmod(filepath.Dir(archive), 0o555)
		defer os.Chmod(filepath.Dir(archive), 0o755)
		if in := detectAt(archive, true, ""); in.How != Archive || in.Writable {
			t.Errorf("a read-only archive: %+v", in)
		}
	}
}

func TestAssetName(t *testing.T) {
	for _, c := range [][3]string{
		{"darwin", "arm64", "trout_0.4.0_darwin_arm64.tar.gz"},
		{"linux", "arm", "trout_0.4.0_linux_armv6.tar.gz"},
		{"windows", "amd64", "trout_0.4.0_windows_amd64.zip"},
	} {
		if got := AssetName("v0.4.0", c[0], c[1]); got != c[2] {
			t.Errorf("AssetName(%s/%s) = %q, want %q", c[0], c[1], got, c[2])
		}
	}
}

// files is what a release archive has, as goreleaser lays it out: flat,
// with a soju folder per platform.
var files = map[string]string{
	"trout":                     "new trout",
	"README.md":                 "new readme",
	"soju-linux-amd64/soju":     "new soju",
	"soju-linux-amd64/sojuctl":  "new sojuctl",
	"soju-darwin-arm64/soju":    "new local soju",
	"soju-darwin-arm64/sojuctl": "new local sojuctl",
}

func tarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg})
		tw.Write([]byte(body))
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func zipped(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, _ := zw.Create(name)
		w.Write([]byte(body))
	}
	zw.Close()
	return buf.Bytes()
}

// fakeGitHub serves a release v0.4.0 with archive as name, and its
// checksum (or sum, if set).
func fakeGitHub(t *testing.T, name string, archive []byte, sum string) Release {
	t.Helper()
	if sum == "" {
		h := sha256.Sum256(archive)
		sum = hex.EncodeToString(h[:])
	}
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	mux.HandleFunc("GET /download/{name}", func(w http.ResponseWriter, r *http.Request) {
		switch r.PathValue("name") {
		case name:
			w.Write(archive)
		case "checksums.txt":
			fmt.Fprintf(w, "0000  trout_0.4.0_other.tar.gz\n%s  %s\n", sum, name)
		default:
			http.NotFound(w, r)
		}
	})
	mux.HandleFunc("GET /latest", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") != "trout/0.3.5" {
			http.Error(w, "no user agent", http.StatusForbidden)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"tag_name": "v0.4.0", "html_url": "https://github.com/MaxRenaud/trout/releases/tag/v0.4.0",
			"assets": []map[string]any{
				{"name": name, "browser_download_url": srv.URL + "/download/" + name, "size": len(archive)},
				{"name": "checksums.txt", "browser_download_url": srv.URL + "/download/checksums.txt", "size": 100},
			},
		})
	})
	old := API
	API = srv.URL + "/latest"
	t.Cleanup(func() { API = old })
	rel, err := Latest(t.Context(), srv.Client(), "trout/0.3.5")
	if err != nil {
		t.Fatal(err)
	}
	if rel.Version != "v0.4.0" || !strings.HasSuffix(rel.URL, "/tag/v0.4.0") || rel.Sizes[name] != int64(len(archive)) {
		t.Fatalf("Latest: %+v", rel)
	}
	return rel
}

// oldInstall is an unpacked v0.3.5 archive, with a file of the user's
// that isn't in the archive.
func oldInstall(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for name := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		os.MkdirAll(filepath.Dir(path), 0o755)
		os.WriteFile(path, []byte("old "+name), 0o755)
	}
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("mine"), 0o644)
	return dir
}

// TestApply replaces an unpacked archive's files with the new release's,
// keeping the old ones as .old until Cleanup, and leaving the user's own
// files alone; tar.gz and zip alike.
func TestApply(t *testing.T) {
	for _, c := range []struct {
		goos, goarch string
		archive      func(*testing.T, map[string]string) []byte
	}{
		{"darwin", "arm64", tarGz},
		{"windows", "amd64", zipped},
	} {
		t.Run(c.goos, func(t *testing.T) {
			these := files
			if c.goos == "windows" {
				these = map[string]string{"trout.exe": "new trout", "soju-windows-amd64/soju.exe": "new local soju"}
			}
			name := AssetName("v0.4.0", c.goos, c.goarch)
			rel := fakeGitHub(t, name, c.archive(t, these), "")
			dir := oldInstall(t)
			os.WriteFile(filepath.Join(dir, "notes.old"), []byte("mine too"), 0o644)
			var said []string
			if err := Apply(t.Context(), http.DefaultClient, rel, dir, c.goos, c.goarch, func(s string) { said = append(said, s) }); err != nil {
				t.Fatal(err)
			}
			for f, body := range these {
				got, _ := os.ReadFile(filepath.Join(dir, filepath.FromSlash(f)))
				if string(got) != body {
					t.Errorf("%s is %q, want %q", f, got, body)
				}
			}
			if got, _ := os.ReadFile(filepath.Join(dir, "trout.old")); c.goos != "windows" && string(got) != "old trout" {
				t.Errorf("trout.old is %q", got)
			}
			if got, _ := os.ReadFile(filepath.Join(dir, "notes.txt")); string(got) != "mine" {
				t.Errorf("the user's notes.txt is %q", got)
			}
			if len(said) != 2 || !strings.HasPrefix(said[0], "downloading "+name) {
				t.Errorf("progress %q", said)
			}
			Cleanup(dir)
			left, _ := filepath.Glob(filepath.Join(dir, "*.old"))
			inner, _ := filepath.Glob(filepath.Join(dir, "soju-*", "*.old"))
			temps, _ := filepath.Glob(filepath.Join(dir, ".trout-*"))
			if len(left) != 1 || filepath.Base(left[0]) != "notes.old" || len(inner)+len(temps) != 0 {
				t.Errorf("left behind %q %q %q, want only the user's notes.old", left, inner, temps)
			}
		})
	}
}

// TestApplyRefuses: a bad checksum or a file that would land outside
// trout's folder changes nothing.
func TestApplyRefuses(t *testing.T) {
	name := AssetName("v0.4.0", "linux", "amd64")
	for _, c := range []struct {
		what    string
		archive []byte
		sum     string
		want    string
	}{
		{"bad checksum", tarGz(t, files), strings.Repeat("ab", 32), "checksum"},
		{"escaping path", tarGz(t, map[string]string{"trout": "x", "../evil": "x"}), "", "outside"},
		{"no trout", tarGz(t, map[string]string{"README.md": "x"}), "", "has no trout"},
	} {
		t.Run(c.what, func(t *testing.T) {
			rel := fakeGitHub(t, name, c.archive, c.sum)
			dir := oldInstall(t)
			err := Apply(t.Context(), http.DefaultClient, rel, dir, "linux", "amd64", func(string) {})
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err %v, want one about %q", err, c.want)
			}
			if got, _ := os.ReadFile(filepath.Join(dir, "trout")); string(got) != "old trout" {
				t.Errorf("trout is %q after a refused update", got)
			}
			if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "evil")); err == nil {
				t.Error("wrote outside trout's folder")
			}
			if temps, _ := filepath.Glob(filepath.Join(dir, ".trout-*")); len(temps) != 0 {
				t.Errorf("left %q", temps)
			}
		})
	}
	fakeGitHub(t, name, nil, "")
	if _, err := Latest(t.Context(), http.DefaultClient, "no agent"); err == nil || !strings.Contains(err.Error(), "403") {
		t.Errorf("Latest took a 403: %v", err)
	}
}

func TestBrew(t *testing.T) {
	args := BrewArgs()
	if len(args) != 2 || args[1][2] != "maxrenaud/tap/trout" {
		t.Fatalf("brew %q", args)
	}
	env := strings.Join(BrewEnv(), "\n")
	for _, want := range []string{"HOMEBREW_NO_AUTOREMOVE=1", "HOMEBREW_NO_INSTALL_CLEANUP=1"} {
		if !strings.Contains(env, want) {
			t.Errorf("brew's environment has no %s", want)
		}
	}
}

// TestApplyWhileRunning replaces trout and its soju while both are
// running, which Windows only allows by renaming them first; Cleanup
// leaves the .old ones while they run and removes them after.
func TestApplyWhileRunning(t *testing.T) {
	goos, exe := runtime.GOOS, ""
	if goos == "windows" {
		exe = ".exe"
	}
	plat := "soju-" + goos + "-" + runtime.GOARCH
	these := map[string]string{"trout" + exe: "new trout", plat + "/soju" + exe: "new soju"}
	name := AssetName("v0.4.0", goos, runtime.GOARCH)
	archive := tarGz
	if goos == "windows" {
		archive = zipped
	}
	rel := fakeGitHub(t, name, archive(t, these), "")
	dir := t.TempDir()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	var running []*exec.Cmd
	for f := range these {
		path := filepath.Join(dir, filepath.FromSlash(f))
		os.MkdirAll(filepath.Dir(path), 0o755)
		b, err := os.ReadFile(self)
		if err != nil {
			t.Fatal(err)
		}
		os.WriteFile(path, b, 0o755)
		cmd := exec.Command(path)
		cmd.Env = append(os.Environ(), "TROUT_TEST_SLEEP=1")
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		running = append(running, cmd)
	}
	stop := func() {
		for _, c := range running {
			c.Process.Kill()
			c.Wait()
		}
		running = nil
	}
	defer stop()
	if err := Apply(t.Context(), http.DefaultClient, rel, dir, goos, runtime.GOARCH, func(string) {}); err != nil {
		t.Fatal(err)
	}
	for f, body := range these {
		if got, _ := os.ReadFile(filepath.Join(dir, filepath.FromSlash(f))); string(got) != body {
			t.Errorf("%s wasn't replaced", f)
		}
	}
	Cleanup(dir) // still running: Windows keeps them, which is fine
	stop()
	time.Sleep(200 * time.Millisecond) // Windows lets go of the files a moment after
	Cleanup(dir)
	left, _ := filepath.Glob(filepath.Join(dir, "*.old"))
	inner, _ := filepath.Glob(filepath.Join(dir, plat, "*.old"))
	if len(left)+len(inner) != 0 {
		t.Errorf("Cleanup left %q %q", left, inner)
	}
}
