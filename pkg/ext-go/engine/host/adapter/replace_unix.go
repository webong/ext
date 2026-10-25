//go:build !windows

package adapter

import "os"

func replaceFile(source, target string) error { return os.Rename(source, target) }
