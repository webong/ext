//go:build linux && amd64

package confine

import "golang.org/x/sys/unix"

// The architecture every call must come from, and the calls that start a process outright.
const (
	seccompArch = unix.AUDIT_ARCH_X86_64
	seccompX32  = true
)

var forkCalls = []uint32{unix.SYS_FORK, unix.SYS_VFORK}

// archHighRisk are high-risk calls this architecture has beyond highRisk.
var archHighRisk = []uint32{unix.SYS_KEXEC_FILE_LOAD}
