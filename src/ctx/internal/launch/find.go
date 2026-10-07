package launch

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func FindReal(name string) (string, error) {
	self, _ := os.Executable()
	selfDir := filepath.Dir(self)
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == "" {
			dir = "."
		}
		absoluteDir, err := filepath.Abs(dir)
		if err == nil && samePath(absoluteDir, selfDir) {
			continue
		}
		for _, executable := range executableNames(name) {
			candidate := filepath.Join(dir, executable)
			info, err := os.Stat(candidate)
			if err != nil || info.IsDir() || !isExecutable(info) {
				continue
			}
			return candidate, nil
		}
	}
	return "", fmt.Errorf("%s CLI not found on PATH", name)
}

func samePath(left, right string) bool {
	return strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
}
