//go:build !windows

package app

import "os"

func replaceManagerRegistry(source, target string) error { return os.Rename(source, target) }
