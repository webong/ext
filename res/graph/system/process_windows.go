//go:build windows

package systemgraph

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

func processHostID(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	// SYSTEM_BOOT_ENVIRONMENT_INFORMATION starts with the boot GUID.
	// Unlike a wall-clock boot estimate, it survives system clock adjustments.
	var info [32]byte
	var size uint32
	if err := windows.NtQuerySystemInformation(windows.SystemBootEnvironmentInformation, unsafe.Pointer(&info[0]), uint32(len(info)), &size); err != nil {
		return "", fmt.Errorf("query boot identity: %w", err)
	}
	return digest(hostEnvironment() + string(info[:16])), nil
}

func windowsProcessEntries(ctx context.Context, limit int) ([]windows.ProcessEntry32, CollectionStatus, error) {
	handle, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, CollectionStatus{}, err
	}
	defer windows.CloseHandle(handle)
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	result := []windows.ProcessEntry32{}
	status := completeProcessStatus()
	err = windows.Process32First(handle, &entry)
	for err == nil {
		if e := ctx.Err(); e != nil {
			return nil, status, e
		}
		if entry.ProcessID != 0 {
			if len(result) >= limit {
				return result, limitedProcessStatus(), nil
			}
			result = append(result, entry)
		}
		err = windows.Process32Next(handle, &entry)
	}
	if !errors.Is(err, windows.ERROR_NO_MORE_FILES) {
		return nil, status, err
	}
	return result, status, nil
}
func platformProcesses(ctx context.Context, limit int) ([]ProcessInfo, CollectionStatus, error) {
	entries, status, err := windowsProcessEntries(ctx, limit)
	if err != nil {
		return nil, status, err
	}
	result := make([]ProcessInfo, 0, len(entries))
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, status, err
		}
		p, err := windowsProcessInfo(entry)
		if err != nil {
			p = newProcessInfo(int(entry.ProcessID))
			p.ParentPID = int(entry.ParentProcessID)
			p.Name = windows.UTF16ToString(entry.ExeFile[:])
			p.Coverage["identity"] = processStatus(err)
		}
		result = append(result, p)
	}
	return result, status, nil
}
func platformProcess(ctx context.Context, pid int) (ProcessInfo, error) {
	entries, _, err := windowsProcessEntries(ctx, 65536)
	if err != nil {
		return ProcessInfo{}, err
	}
	for _, entry := range entries {
		if int(entry.ProcessID) == pid {
			return windowsProcessInfo(entry)
		}
	}
	return ProcessInfo{}, syscall.ESRCH
}
func windowsProcessInfo(entry windows.ProcessEntry32) (ProcessInfo, error) {
	p := newProcessInfo(int(entry.ProcessID))
	p.ParentPID = int(entry.ParentProcessID)
	p.Name = windows.UTF16ToString(entry.ExeFile[:])
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, entry.ProcessID)
	if err != nil {
		return p, err
	}
	defer windows.CloseHandle(h)
	var creation, exit, kernel, user windows.Filetime
	if err = windows.GetProcessTimes(h, &creation, &exit, &kernel, &user); err != nil {
		return p, err
	}
	p.StartID = strconv.FormatUint(uint64(creation.HighDateTime)<<32|uint64(creation.LowDateTime), 10)
	p.Coverage["identity"] = completeProcessStatus()
	buf := make([]uint16, 32768)
	size := uint32(len(buf))
	err = windows.QueryFullProcessImageName(h, 0, &buf[0], &size)
	if err == nil {
		p.Executable = windows.UTF16ToString(buf[:size])
		p.Name = filepath.Base(p.Executable)
	}
	p.Coverage["executable"] = processStatus(err)
	var owner windows.Token
	err = windows.OpenProcessToken(h, windows.TOKEN_QUERY, &owner)
	if err == nil {
		defer owner.Close()
		account, e := owner.GetTokenUser()
		err = e
		if err == nil {
			p.Owner = account.User.Sid.String()
		}
	}
	p.Coverage["owner"] = processStatus(err)
	return p, nil
}

func platformProcessResources(ctx context.Context, p *ProcessInfo, limit int) {
	windowsProcessUsage(p)
	windowsProcessHandles(ctx, p, limit)
	p.Coverage["mapping"] = CollectionStatus{State: "partial", Detail: "loaded modules only; other memory-mapped files are not enumerated"}
	h, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPMODULE|windows.TH32CS_SNAPMODULE32, uint32(p.PID))
	if err != nil {
		p.Coverage["mapping"] = processStatus(err)
	} else {
		entry := windows.ModuleEntry32{Size: uint32(windows.SizeofModuleEntry32)}
		err = windows.Module32First(h, &entry)
		for err == nil {
			if ctx.Err() != nil {
				break
			}
			addProcessResource(p, ProcessResource{Kind: "mapping", Path: windows.UTF16ToString(entry.ExePath[:])}, limit)
			if p.Coverage["mapping"].Detail == "record limit reached" {
				break
			}
			err = windows.Module32Next(h, &entry)
		}
		if err != nil && !errors.Is(err, windows.ERROR_NO_MORE_FILES) {
			p.Coverage["mapping"] = processStatus(err)
		}
		windows.CloseHandle(h)
	}
	windowsProcessMappings(ctx, p, limit)
	p.Coverage["socket"] = CollectionStatus{State: "partial", Detail: "TCP and UDP only; Unix-domain sockets are not enumerated"}
	for _, proto := range []string{"tcp", "udp"} {
		for _, family := range []uint32{windows.AF_INET, windows.AF_INET6} {
			if ctx.Err() != nil {
				return
			}
			table, err := windowsSocketTable(proto, family)
			if err != nil {
				p.Coverage["socket"] = CollectionStatus{State: "partial", Detail: "one or more TCP/UDP tables unavailable"}
				continue
			}
			parseWindowsSockets(p, table, proto, family, limit)
		}
	}
}

type processMemoryCounters struct {
	Size                       uint32
	PageFaultCount             uint32
	PeakWorkingSetSize         uintptr
	WorkingSetSize             uintptr
	QuotaPeakPagedPoolUsage    uintptr
	QuotaPagedPoolUsage        uintptr
	QuotaPeakNonPagedPoolUsage uintptr
	QuotaNonPagedPoolUsage     uintptr
	PagefileUsage              uintptr
	PeakPagefileUsage          uintptr
}

func windowsProcessUsage(p *ProcessInfo) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(p.PID))
	if err != nil {
		p.Coverage["usage"] = processStatus(err)
		return
	}
	defer windows.CloseHandle(h)
	var creation, exit, kernel, user windows.Filetime
	err = windows.GetProcessTimes(h, &creation, &exit, &kernel, &user)
	p.Usage = &ProcessUsage{}
	if err == nil {
		seconds := (float64(uint64(kernel.HighDateTime)<<32|uint64(kernel.LowDateTime)) + float64(uint64(user.HighDateTime)<<32|uint64(user.LowDateTime))) / 1e7
		p.Usage.CPUSeconds = &seconds
	}
	counters := processMemoryCounters{}
	counters.Size = uint32(unsafe.Sizeof(counters))
	proc := windows.NewLazySystemDLL("psapi.dll").NewProc("GetProcessMemoryInfo")
	if e := proc.Find(); e == nil {
		r, _, _ := proc.Call(uintptr(h), uintptr(unsafe.Pointer(&counters)), uintptr(counters.Size))
		if r != 0 {
			rss := uint64(counters.WorkingSetSize)
			p.Usage.ResidentBytes = &rss
		}
	}
	p.Coverage["usage"] = CollectionStatus{State: "partial", Detail: "virtual address size and thread count are not collected; other counters may be inaccessible"}
}

func windowsSocketTable(protocol string, family uint32) ([]byte, error) {
	name, class := "GetExtendedTcpTable", uintptr(5)
	if protocol == "udp" {
		name = "GetExtendedUdpTable"
		class = 1
	}
	proc := windows.NewLazySystemDLL("iphlpapi.dll").NewProc(name)
	if err := proc.Find(); err != nil {
		return nil, err
	}
	var size uint32
	for attempt := 0; attempt < 4; attempt++ {
		if size > 16<<20 {
			return nil, errors.New("socket table exceeds allocation limit")
		}
		buffer := make([]byte, size)
		var ptr uintptr
		if len(buffer) > 0 {
			ptr = uintptr(unsafe.Pointer(&buffer[0]))
		}
		result, _, _ := proc.Call(ptr, uintptr(unsafe.Pointer(&size)), 0, uintptr(family), class, 0)
		if result == 0 {
			if uint64(size) > uint64(len(buffer)) {
				return nil, errors.New("invalid socket table size")
			}
			return buffer[:size], nil
		}
		if syscall.Errno(result) != windows.ERROR_INSUFFICIENT_BUFFER {
			return nil, syscall.Errno(result)
		}
	}
	return nil, errors.New("socket table kept changing")
}
func parseWindowsSockets(p *ProcessInfo, table []byte, protocol string, family uint32, limit int) {
	if len(table) < 4 {
		p.Coverage["socket"] = CollectionStatus{State: "partial", Detail: "short socket table"}
		return
	}
	ipv6 := family == windows.AF_INET6
	tcp := protocol == "tcp"
	width, pidOffset := 12, 8
	if tcp {
		width, pidOffset = 24, 20
	}
	if ipv6 {
		width, pidOffset = 28, 24
		if tcp {
			width, pidOffset = 56, 52
		}
	}
	count := uint64(binary.LittleEndian.Uint32(table))
	if count > uint64((len(table)-4)/width) {
		p.Coverage["socket"] = CollectionStatus{State: "partial", Detail: "truncated socket table"}
		return
	}
	for i := 0; i < int(count); i++ {
		row := table[4+i*width : 4+(i+1)*width]
		if binary.LittleEndian.Uint32(row[pidOffset:]) != uint32(p.PID) {
			continue
		}
		r := ProcessResource{Kind: "socket", Protocol: protocol}
		if ipv6 {
			r.Protocol += "6"
			r.LocalAddress = windowsSocketAddress(row[:16], row[20:24], binary.LittleEndian.Uint32(row[16:20]))
			if tcp {
				r.RemoteAddress = windowsSocketAddress(row[24:40], row[44:48], binary.LittleEndian.Uint32(row[40:44]))
				r.State = windowsTCPState(binary.LittleEndian.Uint32(row[48:52]))
			}
		} else if tcp {
			r.LocalAddress = windowsSocketAddress(row[4:8], row[8:12], 0)
			r.RemoteAddress = windowsSocketAddress(row[12:16], row[16:20], 0)
			r.State = windowsTCPState(binary.LittleEndian.Uint32(row[:4]))
		} else {
			r.LocalAddress = windowsSocketAddress(row[:4], row[4:8], 0)
		}
		addProcessResource(p, r, limit)
	}
}
func windowsSocketAddress(address, port []byte, scope uint32) string {
	ip := net.IP(address).String()
	if scope != 0 {
		ip += "%" + strconv.FormatUint(uint64(scope), 10)
	}
	return net.JoinHostPort(ip, strconv.Itoa(int(binary.BigEndian.Uint16(port[:2]))))
}
func windowsTCPState(state uint32) string {
	names := strings.Fields("UNKNOWN CLOSED LISTEN SYN_SENT SYN_RECEIVED ESTABLISHED FIN_WAIT1 FIN_WAIT2 CLOSE_WAIT CLOSING LAST_ACK TIME_WAIT DELETE_TCB")
	if int(state) < len(names) {
		return names[state]
	}
	return strconv.FormatUint(uint64(state), 10)
}
