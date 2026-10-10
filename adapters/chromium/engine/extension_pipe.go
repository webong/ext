package chromium

// debuggingPipeSupported reports whether this engine can start a browser with
// the debugging-pipe transport on goos. Unix systems pass inherited files and
// Windows passes inherited handles (extension_pipe_unix.go and
// extension_pipe_windows.go). Other systems have no implementation, so
// extension sessions are reported unavailable instead of failing at launch.
func debuggingPipeSupported(goos string) bool {
	switch goos {
	case "windows", "darwin", "linux", "freebsd", "openbsd", "netbsd", "dragonfly", "solaris", "illumos", "aix":
		return true
	}
	return false
}
