// Package identity holds the opaque-identity helpers the collectors share. The values
// are persisted in graph records, so neither function may change its output.
package identity

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"runtime"
)

// Digest returns a stable, opaque 24-character identifier for value.
func Digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:12])
}

// HostEnvironment identifies the host environment a fact was observed in: hostname,
// operating system, architecture, user, home directory and, on Linux, the mount and
// user namespaces.
func HostEnvironment() string {
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "hostname-unavailable"
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "home-unavailable"
	}
	identity := fmt.Sprintf("%s\x00%s\x00%s\x00%d\x00%s", hostname, runtime.GOOS, runtime.GOARCH, os.Getuid(), home)
	if runtime.GOOS == "linux" {
		for _, path := range []string{"/proc/self/ns/mnt", "/proc/self/ns/user"} {
			namespace, err := os.Readlink(path)
			if err != nil {
				namespace = "namespace-unavailable"
			}
			identity += "\x00" + namespace
		}
	}
	return Digest(identity)
}
