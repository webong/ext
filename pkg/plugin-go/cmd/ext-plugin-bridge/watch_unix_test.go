//go:build darwin || linux || freebsd

package main

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestAbruptBridgeDeathCleansGuest(t *testing.T) {
	for _, protocol := range []string{"grpc", "netrpc"} {
		t.Run(protocol, func(t *testing.T) {
			c, exe := selection(t, protocol)
			pidfile := filepath.Join(t.TempDir(), "pid")
			c.Process.Arguments = append(c.Process.Arguments, "--pid-file", pidfile)
			client, cmd := connect(t, exe, configured(t, c))
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if _, err := client.Handshake(ctx); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(pidfile)
			if err != nil {
				t.Fatal(err)
			}
			pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
			if err != nil {
				t.Fatal(err)
			}
			defer syscall.Kill(pid, syscall.SIGKILL)
			if err := cmd.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(3 * time.Second)
			for time.Now().Before(deadline) {
				if err := syscall.Kill(pid, 0); err == syscall.ESRCH {
					return
				}
				time.Sleep(10 * time.Millisecond)
			}
			t.Fatal("watchdog did not reap upstream guest after bridge SIGKILL")
		})
	}
}

func TestNormalAndStartupFailureCleanup(t *testing.T) {
	for _, mode := range []string{"disconnect", "handshake-mismatch"} {
		t.Run(mode, func(t *testing.T) {
			c, exe := selection(t, "grpc")
			pidfile := filepath.Join(t.TempDir(), "pid")
			c.Process.Arguments = append(c.Process.Arguments, "--pid-file", pidfile)
			if mode == "handshake-mismatch" {
				c.Descriptor.Identity.Revision = "wrong"
			}
			client, _ := connect(t, exe, configured(t, c))
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_, err := client.Handshake(ctx)
			if mode == "disconnect" && err != nil {
				t.Fatal(err)
			}
			if mode == "handshake-mismatch" && err == nil {
				t.Fatal("handshake mismatch admitted")
			}
			data, err := os.ReadFile(pidfile)
			if err != nil {
				t.Fatal(err)
			}
			pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
			if err != nil {
				t.Fatal(err)
			}
			_ = client.Close()
			deadline := time.Now().Add(3 * time.Second)
			for time.Now().Before(deadline) {
				if err := syscall.Kill(pid, 0); err == syscall.ESRCH {
					return
				}
				time.Sleep(10 * time.Millisecond)
			}
			_ = syscall.Kill(pid, syscall.SIGKILL)
			t.Fatal("upstream guest survived cleanup")
		})
	}
}
