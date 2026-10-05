//go:build linux

package supervisor

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func processIdentity(pid int) (string, error) {
	root := filepath.Join("/proc", fmt.Sprint(pid))
	payload, err := os.ReadFile(filepath.Join(root, "stat"))
	if err != nil {
		return "", err
	}
	end := strings.LastIndex(string(payload), ") ")
	if end < 0 {
		return "", errors.New("invalid process stat")
	}
	fields := strings.Fields(string(payload)[end+2:])
	if len(fields) <= 19 {
		return "", errors.New("process stat has no start time")
	}
	executable, err := os.Readlink(filepath.Join(root, "exe"))
	if err != nil {
		return "", err
	}
	identity := fmt.Sprintf("%d:%s:%s", pid, fields[19], executable)
	return fmt.Sprintf("%x", sha256.Sum256([]byte(identity))), nil
}
