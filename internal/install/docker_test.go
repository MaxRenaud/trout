//go:build docker

// The install on this computer's own architecture (arm64 on Apple
// silicon, where the Proxmox matrix is amd64 only): a privileged container
// with systemd as PID 1 (dev/systemd), reached over ssh on a mapped port.
// Needs Docker and dev/build-soju. TROUT_DOCKER_ARCH=arm runs it on 32-bit
// ARM (armv7l, like Raspberry Pi OS) instead, emulated by Docker's QEMU.
//
//	go test -tags docker ./internal/install -run TestDockerInstall -v
package install

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestDockerInstall(t *testing.T) {
	arch, platform, image := runtime.GOARCH, "linux/"+runtime.GOARCH, "trout-systemd"
	if os.Getenv("TROUT_DOCKER_ARCH") == "arm" {
		arch, platform, image = "arm", "linux/arm/v7", "trout-systemd-arm"
	}
	if _, _, err := FindBinaries(arch); err != nil {
		t.Fatal(err)
	}
	docker := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("docker", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("docker %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return strings.TrimSpace(string(out))
	}
	docker("build", "-q", "--platform", platform, "-t", image, repo("dev/systemd"))
	id := docker("run", "-d", "--platform", platform, "--privileged", "--cgroupns=host", "-v", "/sys/fs/cgroup:/sys/fs/cgroup:rw",
		"-p", "127.0.0.1::22", "-p", "127.0.0.1::6697", image)
	t.Cleanup(func() { exec.Command("docker", "rm", "-f", id).Run() })
	if arch == "arm" {
		// QEMU's user-mode emulation can't make mount namespaces, so systemd
		// fails soju's sandboxing with 226/NAMESPACE (journald too); a real
		// Pi can. A drop-in turns it off here, and survives setup.sh.
		docker("exec", id, "sh", "-c", "mkdir -p /etc/systemd/system/soju.service.d && printf '[Service]\\n"+
			"ProtectSystem=no\\nProtectHome=no\\nPrivateTmp=no\\nReadWritePaths=\\n' > /etc/systemd/system/soju.service.d/qemu.conf")
	}

	key := filepath.Join(t.TempDir(), "key")
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", key).CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen: %v\n%s", err, out)
	}
	pub, _ := os.ReadFile(key + ".pub")
	docker("exec", id, "sh", "-c", "echo '"+strings.TrimSpace(string(pub))+"' > /home/tester/.ssh/authorized_keys && "+
		"chown tester:tester /home/tester/.ssh/authorized_keys && chmod 600 /home/tester/.ssh/authorized_keys")
	_, sshPort, _ := net.SplitHostPort(docker("port", id, "22"))
	ircAddr := docker("port", id, "6697")
	r := SSH{Host: "tester@127.0.0.1", Options: []string{"-p", sshPort, "-i", key,
		"-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null", "-o", "LogLevel=ERROR"}}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	var f Facts
	var err error
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(time.Second) { // sshd starting
		if f, err = Probe(ctx, r); err == nil || time.Now().After(deadline) {
			break
		}
	}
	if err != nil {
		t.Fatal(err)
	}
	if f.Arch != arch || !f.Systemd || f.Sudo != SudoNoPasswd {
		t.Fatalf("probe: %+v", f)
	}
	t.Logf("%s, %s", f.OS, f.Machine)

	// The container's 6697 is a port here: Check dials it through Dial.
	dial := func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, ircAddr)
	}
	in, err := New(r, f, "127.0.0.1", "max")
	if err != nil {
		t.Fatal(err)
	}
	in.Dial, in.Progress = dial, func(s string) { t.Log(s) }
	for _, step := range []func(context.Context) error{in.StageFiles, in.Setup, in.Check, in.Setup, in.Check} {
		if err := step(ctx); err != nil {
			t.Fatal(err)
		}
	}
	up, _ := Existing(r, f, "127.0.0.1", "max", in.Password, in.Pin, "update")
	up.Dial = dial
	for _, step := range []func(context.Context) error{up.StageFiles, up.Setup, up.Check} {
		if err := step(ctx); err != nil {
			t.Fatal("update:", err)
		}
	}
	gone, _ := Existing(r, f, "127.0.0.1", "max", in.Password, in.Pin, "purge")
	gone.Dial = dial
	if err := gone.StageFiles(ctx); err != nil {
		t.Fatal(err)
	}
	if err := gone.Setup(ctx); err != nil {
		t.Fatal("purge:", err)
	}
	if err := gone.Check(ctx); err == nil {
		t.Fatal("Check passed after a purge")
	}
}
