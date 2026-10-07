//go:build windows

package app

import "os"

func replaceManagerRegistry(source, target string) error {
	if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.Rename(source, target)
}
