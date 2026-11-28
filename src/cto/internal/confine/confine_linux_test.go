//go:build linux

package confine

import (
	"net"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/webong/ext/src/cto/sandbox"
)

// platformGuest performs the Linux-only guest operations.
func platformGuest(args []string) (error, bool) {
	switch args[0] {
	case "unix":
		c, err := net.Dial("unix", args[1])
		if err == nil {
			c.Close()
		}
		return err, true
	case "socketpair":
		fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
		if err == nil {
			unix.Close(fds[0])
			unix.Close(fds[1])
		}
		return err, true
	case "unshare":
		return unix.Unshare(unix.CLONE_NEWUSER), true
	}
	return nil, false
}

// Seccomp closes what Landlock does not: a named Unix socket inside a granted directory is
// still unreachable without network, and the high-risk calls fail whatever the grant.
func TestLinuxSeccomp(t *testing.T) {
	available(t)
	readable, writable := resolved(t, t.TempDir()), resolved(t, t.TempDir())
	socket := filepath.Join(writable, "daemon.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		for {
			c, err := listener.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	if err := os.WriteFile(filepath.Join(readable, "f"), []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	grant := baseGrant(t, readable, writable)
	launch := Launch{Grant: grant, Runner: []string{"lifecycle.fresh"}}
	open := grant
	open.Network = sandbox.Network{Mode: sandbox.NetworkOpen}
	openLaunch := Launch{Grant: open, Runner: []string{"lifecycle.fresh"}}
	cases := []struct {
		name   string
		launch Launch
		args   []string
		want   string
	}{
		{"cannot reach a granted Unix socket without network", launch, []string{"unix", socket}, "refused"},
		{"reaches it with an open network", openLaunch, []string{"unix", socket}, "ok"},
		{"may still make a socket pair", launch, []string{"socketpair"}, "ok"},
		{"cannot make a namespace", launch, []string{"unshare"}, "refused"},
		{"cannot make a namespace with everything granted", openLaunch, []string{"unshare"}, "refused"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			status, out := run(t, c.launch, c.args...)
			if status.Report == nil || status.Report.Driver != "landlock" {
				t.Fatalf("status %+v", status)
			}
			if out != c.want {
				t.Errorf("guest printed %q, want %q", out, c.want)
			}
		})
	}
}

// The filter must kill a call made through another architecture's table and allow threads
// while it refuses new processes.
func TestSeccompFilterShape(t *testing.T) {
	if seccompArch == 0 {
		t.Skip("no seccomp filter for this architecture")
	}
	f := seccompFilter(sandbox.Policy{})
	if f[0].K != 4 || f[1].K != seccompArch || f[2].K != unix.SECCOMP_RET_KILL_PROCESS {
		t.Fatalf("the filter does not check the architecture first: %+v", f[:3])
	}
	if last := f[len(f)-1]; last.K != unix.SECCOMP_RET_ALLOW {
		t.Fatalf("the filter does not end by allowing: %+v", last)
	}
	refused := func(f []unix.SockFilter, nr uint32) bool {
		for i, ins := range f[:len(f)-1] {
			if ins.Code == unix.BPF_JMP|unix.BPF_JEQ|unix.BPF_K && ins.K == nr && f[i+1].K&unix.SECCOMP_RET_ACTION_FULL == unix.SECCOMP_RET_ERRNO {
				return true
			}
		}
		return false
	}
	if !refused(f, unix.SYS_SOCKET) || !refused(f, unix.SYS_CLONE3) || !refused(f, unix.SYS_PTRACE) {
		t.Error("a default grant must refuse socket, clone3 and ptrace")
	}
	g := seccompFilter(sandbox.Policy{Network: sandbox.Network{Mode: sandbox.NetworkOpen}, Process: sandbox.ProcessPolicy{Children: true}})
	if refused(g, unix.SYS_SOCKET) || refused(g, unix.SYS_CLONE3) || !refused(g, unix.SYS_PTRACE) {
		t.Error("an open network with children must allow socket and clone3 but still refuse ptrace")
	}
}
