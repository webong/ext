//go:build !windows

package launch

import "os"

func executableNames(name string) []string { return []string{name} }

func isExecutable(info os.FileInfo) bool { return info.Mode().Perm()&0o111 != 0 }
