//go:build !darwin && !linux

package pkgcmd

func brokenPipeExit(error) bool { return false }
