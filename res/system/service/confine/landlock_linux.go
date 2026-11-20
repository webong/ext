//go:build linux

package confine

import (
	"debug/elf"
	"fmt"
	"os"
	"runtime"
	"slices"
	"unsafe"

	"golang.org/x/sys/unix"

	"github.com/webong/ext/res/sandbox"
)

func platformDrivers() []Driver { return []Driver{landlock{}} }

// landlock confines a process with Landlock for the filesystem, a seccomp filter for the
// network, child processes and the high-risk system calls, no_new_privs, and rlimits. All of it
// is applied to the thread that then calls execve, and the guest inherits it; none of it can be
// undone.
type landlock struct{}

func (landlock) Name() string { return "landlock" }

// landlockBase is what a dynamically linked program needs to start on Linux: the loader, the
// system libraries and their cache, and the harmless devices. Read only; it is added to every
// ruleset and is not part of the grant. A path missing on this host is skipped.
var landlockBase = []string{
	"/usr/lib", "/usr/lib64", "/usr/lib32", "/lib", "/lib64", "/lib32",
	"/etc/ld.so.cache", "/etc/ld.so.conf", "/etc/ld.so.conf.d",
	"/dev/null", "/dev/zero", "/dev/random", "/dev/urandom",
	"/proc/self", "/sys/kernel/mm/transparent_hugepage",
}

// landlockABI returns the Landlock ABI version the kernel offers, or 0 when it offers none.
func landlockABI() int {
	v, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, 0, 0, unix.LANDLOCK_CREATE_RULESET_VERSION)
	if errno != 0 {
		return 0
	}
	return int(v)
}

func (landlock) Capabilities() sandbox.Capabilities {
	enforced := sandbox.Support{Enforced: true}
	missing := sandbox.Support{Reason: sandbox.ReasonUnsupported}
	classes := map[string]sandbox.Support{
		"filesystem":                    enforced,
		"network.none":                  enforced, // seccomp refuses every socket but AF_UNIX
		"network.open":                  enforced,
		"process":                       enforced, // seccomp refuses fork, vfork and clone without CLONE_THREAD
		"devices":                       enforced,
		"environment":                   enforced,
		"resources.memoryBytes":         enforced, // RLIMIT_AS
		"resources.cpuTimeMs":           enforced,
		"resources.openFiles":           enforced,
		"network.ports":                 missing, // Landlock's port rules cover TCP only, not UDP
		"network.proxy":                 missing, // the egress proxy is phase 3
		"resources.milliCpu":            missing, // needs a cgroup
		"resources.pids":                missing, // RLIMIT_NPROC counts every process of the user
		"resources.wallTimeMs":          missing, // only a runner that outlives the guest can bound wall time
		"resources.writeBytes":          missing, // RLIMIT_FSIZE bounds each file, not the total
		"lifecycle.fresh":               missing, // lifecycles are the runner's
		"lifecycle.workspace":           missing,
		"lifecycle.snapshot.filesystem": missing,
		"lifecycle.snapshot.memory":     missing,
	}
	if landlockABI() < 1 {
		classes["filesystem"] = missing
		classes["devices"] = missing
	}
	if unix.Prctl(unix.PR_GET_SECCOMP, 0, 0, 0, 0) != nil {
		for _, c := range []string{"network.none", "process"} {
			classes[c] = missing
		}
	}
	return sandbox.Capabilities{Boundary: sandbox.Confined, Driver: "landlock", Classes: classes}
}

func (landlock) confine(p plan) error {
	// Landlock and no_new_privs apply to the calling thread, and execve keeps only that thread,
	// so everything from here to execve stays on one thread.
	runtime.LockOSThread()
	if err := applyRlimits(p.Grant.Resources); err != nil {
		return err
	}
	if p.Grant.Resources.MemoryBytes != nil {
		limit := unix.Rlimit{Cur: uint64(*p.Grant.Resources.MemoryBytes), Max: uint64(*p.Grant.Resources.MemoryBytes)}
		if err := unix.Setrlimit(unix.RLIMIT_AS, &limit); err != nil {
			return fmt.Errorf("%w: setrlimit", ErrDriver)
		}
	}
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return fmt.Errorf("%w: no_new_privs", ErrDriver)
	}
	if err := applyLandlock(p.Grant, p.Path); err != nil {
		return err
	}
	return applySeccomp(p.Grant)
}

// The access rights a grant maps to, for a directory and for a file. Landlock refuses
// directory rights on a rule for a file.
func landlockRights(access []sandbox.Access, dir bool, abi int) uint64 {
	var r uint64
	if slices.Contains(access, sandbox.Read) {
		r |= unix.LANDLOCK_ACCESS_FS_READ_FILE
		if dir {
			r |= unix.LANDLOCK_ACCESS_FS_READ_DIR
		}
	}
	if slices.Contains(access, sandbox.Write) {
		r |= unix.LANDLOCK_ACCESS_FS_WRITE_FILE
		if abi >= 3 {
			r |= unix.LANDLOCK_ACCESS_FS_TRUNCATE
		}
		if dir {
			r |= unix.LANDLOCK_ACCESS_FS_REMOVE_DIR | unix.LANDLOCK_ACCESS_FS_REMOVE_FILE |
				unix.LANDLOCK_ACCESS_FS_MAKE_DIR | unix.LANDLOCK_ACCESS_FS_MAKE_REG |
				unix.LANDLOCK_ACCESS_FS_MAKE_SOCK | unix.LANDLOCK_ACCESS_FS_MAKE_FIFO |
				unix.LANDLOCK_ACCESS_FS_MAKE_SYM
			if abi >= 2 {
				r |= unix.LANDLOCK_ACCESS_FS_REFER
			}
		}
	}
	if slices.Contains(access, sandbox.Execute) {
		r |= unix.LANDLOCK_ACCESS_FS_EXECUTE | unix.LANDLOCK_ACCESS_FS_READ_FILE
		if dir {
			r |= unix.LANDLOCK_ACCESS_FS_READ_DIR
		}
	}
	return r
}

// handledRights is every filesystem right the ABI knows: all of them are denied unless a rule
// allows them. Device nodes cannot be made at all, and on ABI 5 device ioctls are denied too.
func handledRights(abi int) uint64 {
	r := uint64(unix.LANDLOCK_ACCESS_FS_EXECUTE | unix.LANDLOCK_ACCESS_FS_WRITE_FILE |
		unix.LANDLOCK_ACCESS_FS_READ_FILE | unix.LANDLOCK_ACCESS_FS_READ_DIR |
		unix.LANDLOCK_ACCESS_FS_REMOVE_DIR | unix.LANDLOCK_ACCESS_FS_REMOVE_FILE |
		unix.LANDLOCK_ACCESS_FS_MAKE_CHAR | unix.LANDLOCK_ACCESS_FS_MAKE_DIR |
		unix.LANDLOCK_ACCESS_FS_MAKE_REG | unix.LANDLOCK_ACCESS_FS_MAKE_SOCK |
		unix.LANDLOCK_ACCESS_FS_MAKE_FIFO | unix.LANDLOCK_ACCESS_FS_MAKE_BLOCK |
		unix.LANDLOCK_ACCESS_FS_MAKE_SYM)
	if abi >= 2 {
		r |= unix.LANDLOCK_ACCESS_FS_REFER
	}
	if abi >= 3 {
		r |= unix.LANDLOCK_ACCESS_FS_TRUNCATE
	}
	if abi >= 5 {
		r |= unix.LANDLOCK_ACCESS_FS_IOCTL_DEV
	}
	return r
}

func applyLandlock(grant sandbox.Policy, guestPath string) error {
	abi := landlockABI()
	if abi < 1 {
		return fmt.Errorf("%w: landlock", ErrDriver)
	}
	handled := handledRights(abi)
	attr := unix.LandlockRulesetAttr{Access_fs: handled}
	fd, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, uintptr(unsafe.Pointer(&attr)), unsafe.Sizeof(attr.Access_fs), 0)
	if errno != 0 {
		return fmt.Errorf("%w: landlock_create_ruleset", ErrDriver)
	}
	ruleset := int(fd)
	defer unix.Close(ruleset)
	add := func(path string, access []sandbox.Access) error {
		target, err := unix.Open(path, unix.O_PATH|unix.O_CLOEXEC, 0)
		if err != nil {
			if os.IsNotExist(err) {
				return nil // a granted path that does not exist yet grants nothing
			}
			return fmt.Errorf("%w: open", ErrDriver)
		}
		defer unix.Close(target)
		var st unix.Stat_t
		if err := unix.Fstat(target, &st); err != nil {
			return fmt.Errorf("%w: stat", ErrDriver)
		}
		rights := landlockRights(access, st.Mode&unix.S_IFMT == unix.S_IFDIR, abi) & handled
		if rights == 0 {
			return nil
		}
		rule := unix.LandlockPathBeneathAttr{Allowed_access: rights, Parent_fd: int32(target)}
		if _, _, errno := unix.Syscall6(unix.SYS_LANDLOCK_ADD_RULE, uintptr(ruleset), unix.LANDLOCK_RULE_PATH_BENEATH, uintptr(unsafe.Pointer(&rule)), 0, 0, 0); errno != 0 {
			return fmt.Errorf("%w: landlock_add_rule", ErrDriver)
		}
		return nil
	}
	for _, path := range landlockBase {
		if err := add(path, []sandbox.Access{sandbox.Read}); err != nil {
			return err
		}
	}
	// The kernel opens a dynamically linked guest's loader for execution, so that one file
	// (and only it) is executable as well.
	if loader := interpreter(guestPath); loader != "" {
		if err := add(loader, []sandbox.Access{sandbox.Read, sandbox.Execute}); err != nil {
			return err
		}
	}
	for _, entry := range grant.Filesystem {
		if err := add(entry.Path, entry.Access); err != nil {
			return err
		}
	}
	if _, _, errno := unix.Syscall(unix.SYS_LANDLOCK_RESTRICT_SELF, uintptr(ruleset), 0, 0); errno != 0 {
		return fmt.Errorf("%w: landlock_restrict_self", ErrDriver)
	}
	return nil
}

// interpreter is the ELF loader a guest names (PT_INTERP), or "" for a static binary, a script
// or a file that is not ELF.
func interpreter(path string) string {
	f, err := elf.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	for _, prog := range f.Progs {
		if prog.Type != elf.PT_INTERP {
			continue
		}
		buf := make([]byte, prog.Filesz)
		if _, err := prog.ReadAt(buf, 0); err != nil {
			return ""
		}
		for i, c := range buf {
			if c == 0 {
				buf = buf[:i]
				break
			}
		}
		return string(buf)
	}
	return ""
}
