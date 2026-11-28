//go:build linux && arm64

package confine

import "golang.org/x/sys/unix"

// The architecture every call must come from. arm64 has no fork or vfork call; every process
// starts with clone or clone3.
const (
	seccompArch = unix.AUDIT_ARCH_AARCH64
	seccompX32  = false
)

var forkCalls []uint32

// archHighRisk are high-risk calls this architecture has beyond highRisk.
var archHighRisk = []uint32{unix.SYS_KEXEC_FILE_LOAD}
