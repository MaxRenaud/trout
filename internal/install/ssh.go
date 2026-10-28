package install

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// Runner runs shell commands on the server. SSH is the real one; tests
// use fakes.
type Runner interface {
	Run(ctx context.Context, c Cmd) error
}

// Cmd is one remote shell command line. Stdout, if set, gets its output
// as it comes; TTY asks for a terminal, which some sudo setups insist on.
type Cmd struct {
	Line   string
	Stdin  io.Reader
	Stdout io.Writer
	TTY    bool
}

// SSH runs commands with the system's ssh, so ~/.ssh/config, the agent,
// ProxyJump, known_hosts and hardware keys work exactly as they do in a
// terminal. It never prompts: BatchMode makes a missing key or an unknown
// host key an error (SSHError) instead.
type SSH struct {
	Host    string   // a Host from ~/.ssh/config, or [user@]host
	Options []string // more ssh arguments, before the host (tests: -F, -i)
	Program string   // "ssh" if empty
}

func (s SSH) program() string {
	if s.Program == "" {
		return "ssh"
	}
	return s.Program
}

// Args are ssh's arguments for running line, also used to hand the
// terminal to ssh when it needs to ask something (see Interactive).
func (s SSH) args(line string, tty, batch bool) []string {
	args := []string{"-o", "ConnectTimeout=15", "-o", "ServerAliveInterval=15"}
	if batch {
		args = append(args, "-o", "BatchMode=yes")
	}
	if tty {
		args = append(args, "-tt")
	} else {
		args = append(args, "-T")
	}
	args = append(args, s.Options...)
	return append(args, s.Host, "--", line)
}

func (s SSH) Run(ctx context.Context, c Cmd) error {
	cmd := exec.CommandContext(ctx, s.program(), s.args(c.Line, c.TTY, true)...)
	cmd.Stdin, cmd.Stdout = c.Stdin, c.Stdout
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return nil
	case ctx.Err() != nil:
		return ctx.Err()
	case errors.As(err, &exit) && exit.ExitCode() == 255:
		return sshError(stderr.String())
	case errors.As(err, &exit):
		return &RemoteError{Line: c.Line, Code: exit.ExitCode(), Stderr: strings.TrimSpace(stderr.String())}
	}
	return fmt.Errorf("running ssh: %w", err)
}

// Interactive is the ssh command that just logs in, for when SSH needs
// you: accepting a new host key, unlocking a key. trout hands it the
// terminal (tea.ExecProcess) and carries on once it's worked.
func (s SSH) Interactive() *exec.Cmd {
	return exec.Command(s.program(), s.args("true", false, false)...)
}

// Resolve asks ssh what Host turns into, after ~/.ssh/config: the real
// host name (for the bouncer's address), the user and the port. It runs
// locally (ssh -G) and connects nowhere.
func (s SSH) Resolve(ctx context.Context) (host, user string, err error) {
	args := append(append([]string{}, s.Options...), "-G", s.Host)
	out, err := exec.CommandContext(ctx, s.program(), args...).Output()
	if err != nil {
		return "", "", fmt.Errorf("ssh -G %s: %w", s.Host, err)
	}
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		k, v, _ := strings.Cut(sc.Text(), " ")
		switch k {
		case "hostname":
			host = v
		case "user":
			user = v
		}
	}
	if host == "" {
		return "", "", fmt.Errorf("ssh -G %s didn't say a host name", s.Host)
	}
	return host, user, nil
}

// SSHError is ssh itself failing (exit status 255), not the command.
type SSHError struct {
	Kind   SSHFailure
	Stderr string
}

type SSHFailure int

const (
	SSHOther       SSHFailure = iota
	SSHHostKey                // unknown or changed host key: BatchMode won't ask
	SSHAuth                   // no key the server takes (or one that's locked)
	SSHUnreachable            // no such host, refused, timed out
)

func (e *SSHError) Error() string {
	switch e.Kind {
	case SSHHostKey:
		return "ssh doesn't know this server's host key yet: " + e.Stderr
	case SSHAuth:
		return "ssh couldn't log in with a key: " + e.Stderr
	case SSHUnreachable:
		return "can't reach the server: " + e.Stderr
	}
	return "ssh failed: " + e.Stderr
}

// NeedsYou reports whether running ssh in the terminal once could fix
// it: a host key to accept, or a key to unlock.
func (e *SSHError) NeedsYou() bool { return e.Kind == SSHHostKey || e.Kind == SSHAuth }

func sshError(stderr string) *SSHError {
	stderr = strings.TrimSpace(stderr)
	e := &SSHError{Stderr: stderr}
	low := strings.ToLower(stderr)
	switch {
	case strings.Contains(low, "host key verification failed"),
		strings.Contains(low, "remote host identification has changed"):
		e.Kind = SSHHostKey
	case strings.Contains(low, "permission denied"),
		strings.Contains(low, "too many authentication failures"):
		e.Kind = SSHAuth
	case strings.Contains(low, "could not resolve"),
		strings.Contains(low, "connection refused"),
		strings.Contains(low, "timed out"),
		strings.Contains(low, "no route to host"),
		strings.Contains(low, "network is unreachable"):
		e.Kind = SSHUnreachable
	}
	return e
}

// RemoteError is a command that ran on the server and failed.
type RemoteError struct {
	Line   string
	Code   int
	Stderr string
}

func (e *RemoteError) Error() string {
	if e.Stderr == "" {
		return fmt.Sprintf("%q exited with %d", e.Line, e.Code)
	}
	return fmt.Sprintf("%q exited with %d: %s", e.Line, e.Code, e.Stderr)
}

// output runs line and returns what it printed.
func output(ctx context.Context, r Runner, line string) (string, error) {
	var out bytes.Buffer
	err := r.Run(ctx, Cmd{Line: line, Stdout: &out})
	return out.String(), err
}
