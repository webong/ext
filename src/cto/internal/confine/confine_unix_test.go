//go:build darwin || linux

package confine

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/webong/ext/src/cto/sandbox"
)

// The test binary plays three parts, chosen by EXT_CONFINE_ROLE: the test itself; the launcher,
// which reads a Launch from EXT_CONFINE_LAUNCH and calls Exec (as `ext sandbox exec` does); and
// the guest, which the launcher becomes and which tries one operation and prints how it went.
func TestMain(m *testing.M) {
	switch os.Getenv("EXT_CONFINE_ROLE") {
	case "launch":
		var l Launch
		if err := json.Unmarshal([]byte(os.Getenv("EXT_CONFINE_LAUNCH")), &l); err != nil {
			fmt.Fprintln(os.Stderr, "bad launch:", err)
			os.Exit(2)
		}
		_, err := Exec(l, os.Stderr)
		fmt.Fprintln(os.Stderr, "exec failed:", Code(err))
		os.Exit(126)
	case "guest":
		os.Exit(guest(os.Args[1:]))
	}
	os.Exit(m.Run())
}

// guest performs one operation and prints "ok" or "refused".
func guest(args []string) int {
	report := func(err error) int {
		if err != nil {
			fmt.Fprintln(os.Stderr, "guest:", err)
			fmt.Println("refused")
			return 0
		}
		fmt.Println("ok")
		return 0
	}
	switch args[0] {
	case "read":
		_, err := os.ReadFile(args[1])
		return report(err)
	case "write":
		return report(os.WriteFile(args[1], []byte("x"), 0o600))
	case "connect":
		c, err := net.Dial("tcp", args[1])
		if err == nil {
			c.Close()
		}
		return report(err)
	case "spawn":
		return report(exec.Command("/bin/echo", "child").Run())
	case "env":
		fmt.Println(strings.Join(os.Environ(), ";"))
		return 0
	}
	if err, ok := platformGuest(args); ok {
		return report(err)
	}
	return 3
}

func resolved(t *testing.T, path string) string {
	t.Helper()
	p, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// run launches the test binary as the guest under grant and returns the status line and what
// the guest printed.
func run(t *testing.T, l Launch, args ...string) (Status, string) {
	t.Helper()
	self := resolved(t, os.Args[0])
	l.Path = self
	l.Args = args
	l.Env = append(l.Env, "EXT_CONFINE_ROLE=guest")
	encoded, _ := json.Marshal(l)
	cmd := exec.Command(self)
	cmd.Env = []string{"EXT_CONFINE_ROLE=launch", "EXT_CONFINE_LAUNCH=" + string(encoded)}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	_ = cmd.Run()
	var status Status
	line, _, _ := bufio.NewReader(bytes.NewReader(stderr.Bytes())).ReadLine()
	if err := json.Unmarshal(line, &status); err != nil {
		t.Fatalf("no status line: %q (stdout %q)", stderr.String(), stdout.String())
	}
	if testing.Verbose() {
		_, rest, _ := strings.Cut(stderr.String(), "\n")
		t.Logf("exit %d, stderr after the status %q", cmd.ProcessState.ExitCode(), rest)
	}
	return status, strings.TrimSpace(stdout.String())
}

func available(t *testing.T) {
	t.Helper()
	if _, err := pick(""); err != nil {
		t.Skip("no confinement driver on this host")
	}
}

func baseGrant(t *testing.T, readable, writable string) sandbox.Policy {
	self := filepath.Dir(resolved(t, os.Args[0]))
	return sandbox.Policy{
		Filesystem: []sandbox.Path{
			{Path: filepath.ToSlash(self), Access: []sandbox.Access{sandbox.Read, sandbox.Execute}},
			{Path: filepath.ToSlash(readable), Access: []sandbox.Access{sandbox.Read}},
			{Path: filepath.ToSlash(writable), Access: []sandbox.Access{sandbox.Read, sandbox.Write}},
		},
		Network:   sandbox.Network{Mode: sandbox.NetworkNone},
		Lifecycle: sandbox.Lifecycle{Mode: sandbox.LifecycleFresh},
	}
}

func TestConfinedGuest(t *testing.T) {
	available(t)
	readable, writable, outside := resolved(t, t.TempDir()), resolved(t, t.TempDir()), resolved(t, t.TempDir())
	for _, dir := range []string{readable, outside} {
		if err := os.WriteFile(filepath.Join(dir, "f"), []byte("data"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
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
	grant := baseGrant(t, readable, writable)
	launch := Launch{Grant: grant, Runner: []string{"lifecycle.fresh"}}
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"reads a granted file", []string{"read", filepath.Join(readable, "f")}, "ok"},
		{"cannot read outside the grant", []string{"read", filepath.Join(outside, "f")}, "refused"},
		{"cannot write a read-only grant", []string{"write", filepath.Join(readable, "g")}, "refused"},
		{"writes a writable grant", []string{"write", filepath.Join(writable, "g")}, "ok"},
		{"cannot write outside the grant", []string{"write", filepath.Join(outside, "g")}, "refused"},
		{"cannot reach the network", []string{"connect", listener.Addr().String()}, "refused"},
		{"cannot start a child", []string{"spawn"}, "refused"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			status, out := run(t, launch, c.args...)
			if status.Report == nil || status.Report.Boundary != sandbox.Confined {
				t.Fatalf("status %+v", status)
			}
			if out != c.want {
				t.Errorf("guest printed %q, want %q", out, c.want)
			}
		})
	}
	t.Run("an open network and children when granted", func(t *testing.T) {
		g := grant
		g.Network = sandbox.Network{Mode: sandbox.NetworkOpen}
		g.Process = sandbox.ProcessPolicy{Children: true}
		g.Filesystem = append(g.Filesystem, sandbox.Path{Path: "/bin", Access: []sandbox.Access{sandbox.Read, sandbox.Execute}})
		l := Launch{Grant: g, Runner: []string{"lifecycle.fresh"}}
		if _, out := run(t, l, "connect", listener.Addr().String()); out != "ok" {
			t.Errorf("connect: %q", out)
		}
		if _, out := run(t, l, "spawn"); out != "ok" {
			t.Errorf("spawn: %q", out)
		}
	})
	t.Run("the environment is exactly the base, the grant and its secrets", func(t *testing.T) {
		g := grant
		g.Environment = sandbox.Environment{Entries: []string{"LANG=C"}, Secrets: []sandbox.Secret{{Name: "TOKEN", Ref: "test:token"}}}
		l := Launch{Grant: g, Runner: []string{"lifecycle.fresh"}, Env: []string{"PATH=/usr/bin"}, Secrets: map[string]string{"TOKEN": "s3cret"}}
		_, out := run(t, l, "env")
		if out != "PATH=/usr/bin;EXT_CONFINE_ROLE=guest;LANG=C;TOKEN=s3cret" {
			t.Errorf("environment %q", out)
		}
	})
}

func TestRefusedLaunches(t *testing.T) {
	available(t)
	dir := resolved(t, t.TempDir())
	grant := baseGrant(t, dir, resolved(t, t.TempDir()))
	cases := []struct {
		name string
		l    Launch
		code string
	}{
		{"lifecycle not enforced by anyone", Launch{Grant: grant}, "unsupported"},
		{"lifecycle accepted unenforced", Launch{Grant: grant, Accept: []string{"lifecycle.fresh"}}, ""},
		{"a class nobody enforces", Launch{Grant: withMilliCPU(grant), Runner: []string{"lifecycle.fresh"}}, "unsupported"},
		{"the guest is not executable", Launch{Grant: sandbox.Policy{Filesystem: []sandbox.Path{{Path: dir, Access: []sandbox.Access{sandbox.Read}}}}, Runner: []string{"lifecycle.fresh"}}, "denied"},
		{"an unknown runner class", Launch{Grant: grant, Runner: []string{"lifecycle.fresh", "everything"}}, "invalid"},
		{"an unknown driver", Launch{Grant: grant, Runner: []string{"lifecycle.fresh"}, Driver: "nope"}, "unsupported"},
		{"a secret without a value", Launch{Grant: withSecret(grant), Runner: []string{"lifecycle.fresh"}}, "invalid"},
		{"a named device", Launch{Grant: withDevice(grant), Runner: []string{"lifecycle.fresh"}}, "unsupported"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			l := c.l
			l.Path = resolved(t, os.Args[0])
			_, err := Prepare(l)
			if got := Code(err); got != c.code {
				t.Errorf("code %q, want %q (%v)", got, c.code, err)
			}
		})
	}
	t.Run("a refusal is written as a status line", func(t *testing.T) {
		status, out := run(t, Launch{Grant: grant})
		if status.Code != "unsupported" || status.Report != nil || out != "" {
			t.Errorf("status %+v, guest printed %q", status, out)
		}
	})
}

// withMilliCPU bounds CPU shares, which needs a cgroup: no driver enforces it.
func withMilliCPU(p sandbox.Policy) sandbox.Policy {
	v := int64(500)
	p.Resources.MilliCPU = &v
	return p
}

func withSecret(p sandbox.Policy) sandbox.Policy {
	p.Environment.Secrets = []sandbox.Secret{{Name: "TOKEN", Ref: "r"}}
	return p
}

func withDevice(p sandbox.Policy) sandbox.Policy {
	p.Devices = []string{"gpu/0"}
	return p
}
