//go:build darwin || linux

package nativeplugin

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"syscall"
)

const peerSocketsSupported = true

func checkPeerSocket(path string) error {
	dir, err := os.Lstat(filepath.Dir(path))
	if err != nil {
		return err
	}
	socket, err := os.Lstat(path)
	if err != nil {
		return err
	}
	sameUser := func(info os.FileInfo) bool {
		stat, ok := info.Sys().(*syscall.Stat_t)
		return ok && uint32(stat.Uid) == uint32(os.Geteuid())
	}
	if !dir.IsDir() || dir.Mode().Perm()&0077 != 0 || !sameUser(dir) ||
		socket.Mode()&os.ModeSocket == 0 || !sameUser(socket) {
		return fmt.Errorf("peer socket must be in a private directory owned by this user")
	}
	return nil
}

// authenticatePeer uses kernel credentials as well as the endpoint's filesystem
// ownership so moving an ancestor path cannot substitute a different user.
func authenticatePeer(conn net.Conn) error {
	raw, err := conn.(*net.UnixConn).SyscallConn()
	if err != nil {
		return err
	}
	var uid uint32
	if controlErr := raw.Control(func(fd uintptr) {
		uid, err = socketPeerUID(int(fd))
	}); controlErr != nil {
		return controlErr
	}
	if err != nil {
		return err
	}
	if uid != uint32(os.Geteuid()) {
		return fmt.Errorf("peer connection belongs to another user")
	}
	return nil
}
