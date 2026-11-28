//go:build linux

package confine

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/unix"

	"github.com/webong/ext/src/cto/sandbox"
)

// highRisk are the system calls no guest may make, whatever it is granted: they debug or
// read other processes, change mounts, namespaces or the kernel, load programs into the
// kernel, or (io_uring) perform I/O without passing through this filter. Each fails with
// EPERM.
var highRisk = []uint32{
	unix.SYS_PTRACE, unix.SYS_PROCESS_VM_READV, unix.SYS_PROCESS_VM_WRITEV,
	unix.SYS_MOUNT, unix.SYS_UMOUNT2, unix.SYS_PIVOT_ROOT, unix.SYS_CHROOT,
	unix.SYS_FSOPEN, unix.SYS_FSCONFIG, unix.SYS_FSMOUNT, unix.SYS_FSPICK,
	unix.SYS_MOVE_MOUNT, unix.SYS_OPEN_TREE, unix.SYS_MOUNT_SETATTR,
	unix.SYS_SETNS, unix.SYS_UNSHARE,
	unix.SYS_BPF, unix.SYS_PERF_EVENT_OPEN, unix.SYS_USERFAULTFD,
	unix.SYS_KEXEC_LOAD, unix.SYS_REBOOT,
	unix.SYS_INIT_MODULE, unix.SYS_FINIT_MODULE, unix.SYS_DELETE_MODULE,
	unix.SYS_SWAPON, unix.SYS_SWAPOFF, unix.SYS_ACCT, unix.SYS_QUOTACTL,
	unix.SYS_KEYCTL, unix.SYS_ADD_KEY, unix.SYS_REQUEST_KEY,
	unix.SYS_OPEN_BY_HANDLE_AT, unix.SYS_NAME_TO_HANDLE_AT,
	unix.SYS_IO_URING_SETUP, unix.SYS_IO_URING_ENTER, unix.SYS_IO_URING_REGISTER,
}

// seccompFilter is the classic BPF program for a grant. It kills a process that makes a call
// through another architecture's system call table (the usual way around a filter), refuses
// the high-risk calls, then:
//
//   - with no network, refuses socket() for every domain, AF_UNIX included: Landlock does not
//     mediate connecting to a named Unix socket, so allowing it would reach local daemons.
//     socketpair() stays allowed; its sockets have no name and reach nothing.
//   - without children, refuses fork and vfork, clone3 with ENOSYS (so the C library falls back
//     to clone), and clone unless it makes a thread (CLONE_THREAD).
//
// Everything else is allowed: the filesystem is Landlock's, and resources are rlimits.
func seccompFilter(grant sandbox.Policy) []unix.SockFilter {
	stmt := func(code uint16, k uint32) unix.SockFilter { return unix.SockFilter{Code: code, K: k} }
	jump := func(code uint16, k uint32, jt, jf uint8) unix.SockFilter {
		return unix.SockFilter{Code: code, Jt: jt, Jf: jf, K: k}
	}
	const (
		load    = unix.BPF_LD | unix.BPF_W | unix.BPF_ABS
		equal   = unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K
		atLeast = unix.BPF_JMP | unix.BPF_JGE | unix.BPF_K
		anySet  = unix.BPF_JMP | unix.BPF_JSET | unix.BPF_K
		ret     = unix.BPF_RET | unix.BPF_K
	)
	errno := func(e unix.Errno) uint32 { return unix.SECCOMP_RET_ERRNO | uint32(e) }
	// struct seccomp_data: nr at 0, arch at 4, args[i] at 16+8i (the low half first on these
	// little-endian architectures).
	var f []unix.SockFilter
	f = append(f,
		stmt(load, 4),
		jump(equal, seccompArch, 1, 0),
		stmt(ret, unix.SECCOMP_RET_KILL_PROCESS),
		stmt(load, 0),
	)
	if seccompX32 {
		// x32 calls share the x86-64 table with bit 30 set.
		f = append(f, jump(atLeast, 0x40000000, 0, 1), stmt(ret, unix.SECCOMP_RET_KILL_PROCESS))
	}
	refuse := func(nr uint32, e unix.Errno) {
		f = append(f, jump(equal, nr, 0, 1), stmt(ret, errno(e)))
	}
	for _, nr := range append(highRisk[:len(highRisk):len(highRisk)], archHighRisk...) {
		refuse(nr, unix.EPERM)
	}
	if grant.Network.Mode != sandbox.NetworkOpen {
		refuse(unix.SYS_SOCKET, unix.EACCES)
	}
	if !grant.Process.Children {
		for _, nr := range forkCalls {
			refuse(nr, unix.EPERM)
		}
		refuse(unix.SYS_CLONE3, unix.ENOSYS)
		// clone: allowed only with CLONE_THREAD in its flags, the first argument.
		f = append(f,
			jump(equal, unix.SYS_CLONE, 0, 3),
			stmt(load, 16),
			jump(anySet, unix.CLONE_THREAD, 1, 0),
			stmt(ret, errno(unix.EPERM)),
		)
	}
	return append(f, stmt(ret, unix.SECCOMP_RET_ALLOW))
}

// applySeccomp installs the grant's filter on the calling thread, which no_new_privs allows
// without privilege. It must run after Landlock, whose calls the filter does not refuse anyway.
func applySeccomp(grant sandbox.Policy) error {
	if seccompArch == 0 {
		// Reached only when the caller accepted running without network.none and process.
		return nil
	}
	filter := seccompFilter(grant)
	program := unix.SockFprog{Len: uint16(len(filter)), Filter: &filter[0]}
	if err := unix.Prctl(unix.PR_SET_SECCOMP, unix.SECCOMP_MODE_FILTER, uintptr(unsafe.Pointer(&program)), 0, 0); err != nil {
		return fmt.Errorf("%w: seccomp", ErrDriver)
	}
	return nil
}
