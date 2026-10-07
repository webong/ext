//go:build !windows

package mod

import "os"

func replaceFile(source, target string) error { return os.Rename(source, target) }
