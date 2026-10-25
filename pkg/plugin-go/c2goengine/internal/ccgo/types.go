package ccgo

// ProcessOptions launches a reviewed executable directly, without a shell. Environment is explicit
// and never inherits the embedding process environment.
type ProcessOptions struct {
	Executable             string
	Arguments, Environment []string
}

// LaunchOptions starts a reviewed executable the way an installed package is run: like
// ProcessOptions, but with a working directory, bounded capture of the child's stderr and a scrubbed
// base environment of PATH. The environment entries are added to it (an entry named PATH replaces
// it). Executable must be the absolute path of a regular file.
type LaunchOptions struct {
	ProcessOptions
	// Directory is the child's working directory; empty inherits the host's. It must be absolute.
	Directory string
	// StderrBytes bounds the retained stderr; zero retains 4096 bytes.
	StderrBytes int
}
