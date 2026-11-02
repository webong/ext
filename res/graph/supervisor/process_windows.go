//go:build windows

package supervisor

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"unsafe"
)

const jobObjectExtendedLimitInformation = 9
const jobObjectLimitKillOnJobClose = 0x2000

var (
	kernel32                 = syscall.NewLazyDLL("kernel32.dll")
	createJobObject          = kernel32.NewProc("CreateJobObjectW")
	setInformationJobObject  = kernel32.NewProc("SetInformationJobObject")
	assignProcessToJobObject = kernel32.NewProc("AssignProcessToJobObject")
	terminateJobObject       = kernel32.NewProc("TerminateJobObject")
)

type jobBasicLimitInformation struct {
	PerProcessUserTimeLimit int64
	PerJobUserTimeLimit     int64
	LimitFlags              uint32
	MinimumWorkingSetSize   uintptr
	MaximumWorkingSetSize   uintptr
	ActiveProcessLimit      uint32
	Affinity                uintptr
	PriorityClass           uint32
	SchedulingClass         uint32
}

type jobIoCounters struct {
	ReadOperationCount  uint64
	WriteOperationCount uint64
	OtherOperationCount uint64
	ReadTransferCount   uint64
	WriteTransferCount  uint64
	OtherTransferCount  uint64
}

type jobExtendedLimitInformation struct {
	BasicLimitInformation jobBasicLimitInformation
	IoInfo                jobIoCounters
	ProcessMemoryLimit    uintptr
	JobMemoryLimit        uintptr
	PeakProcessMemoryUsed uintptr
	PeakJobMemoryUsed     uintptr
}

type windowsProcessControl struct{ job syscall.Handle }

func newProcessControl(cmd *exec.Cmd) (processControl, error) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= syscall.CREATE_NEW_PROCESS_GROUP
	handle, _, err := createJobObject.Call(0, 0)
	if handle == 0 {
		return nil, err
	}
	c := &windowsProcessControl{job: syscall.Handle(handle)}
	limits := jobExtendedLimitInformation{}
	limits.BasicLimitInformation.LimitFlags = jobObjectLimitKillOnJobClose
	result, _, err := setInformationJobObject.Call(handle, jobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), unsafe.Sizeof(limits))
	if result == 0 {
		_ = c.Close()
		return nil, err
	}
	return c, nil
}

func (c *windowsProcessControl) Attach(process *os.Process) error {
	const processSetQuota = 0x0100
	handle, err := syscall.OpenProcess(processSetQuota|syscall.PROCESS_TERMINATE|syscall.PROCESS_QUERY_INFORMATION, false, uint32(process.Pid))
	if err != nil {
		return err
	}
	defer syscall.CloseHandle(handle)
	result, _, err := assignProcessToJobObject.Call(uintptr(c.job), uintptr(handle))
	if result == 0 {
		return err
	}
	return nil
}

func (c *windowsProcessControl) Terminate(bool) error {
	result, _, err := terminateJobObject.Call(uintptr(c.job), 1)
	if result == 0 {
		return err
	}
	return nil
}

func (c *windowsProcessControl) Close() error {
	if c.job == 0 {
		return nil
	}
	err := syscall.CloseHandle(c.job)
	c.job = 0
	return err
}

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	handle, err := syscall.OpenProcess(syscall.PROCESS_QUERY_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer syscall.CloseHandle(handle)
	var code uint32
	return syscall.GetExitCodeProcess(handle, &code) == nil && code == 259
}

func processIdentity(pid int) (string, error) {
	handle, err := syscall.OpenProcess(syscall.PROCESS_QUERY_INFORMATION, false, uint32(pid))
	if err != nil {
		return "", err
	}
	defer syscall.CloseHandle(handle)
	var created, exited, kernel, user syscall.Filetime
	if err := syscall.GetProcessTimes(handle, &created, &exited, &kernel, &user); err != nil {
		return "", err
	}
	if created.HighDateTime == 0 && created.LowDateTime == 0 {
		return "", errors.New("process creation time is unavailable")
	}
	return fmt.Sprintf("%d:%08x%08x", pid, created.HighDateTime, created.LowDateTime), nil
}

func terminateOrphan(pid int, identity string) error {
	if !processAlive(pid) {
		return nil
	}
	current, err := processIdentity(pid)
	if err != nil || current != identity {
		return errors.New("orphan process identity changed")
	}
	// Windows jobs normally terminate the process tree when the previous
	// supervisor exits. This also handles a process from an older state file.
	if err := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(pid)).Run(); err != nil && processAlive(pid) {
		return err
	}
	return nil
}
