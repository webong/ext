//go:build darwin

package systemgraph

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

func processHostID(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	boot, err := unix.Sysctl("kern.bootsessionuuid")
	if err != nil {
		return "", err
	}
	if boot == "" {
		return "", errors.New("boot identity unavailable")
	}
	return digest(hostEnvironment() + boot), nil
}

func platformProcesses(ctx context.Context, limit int) ([]ProcessInfo, CollectionStatus, error) {
	records, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil, CollectionStatus{}, err
	}
	result := []ProcessInfo{}
	status := completeProcessStatus()
	for _, record := range records {
		if err := ctx.Err(); err != nil {
			return nil, status, err
		}
		if record.Proc.P_pid <= 0 {
			continue
		}
		if len(result) >= limit {
			status = limitedProcessStatus()
			break
		}
		p, err := platformProcess(ctx, int(record.Proc.P_pid))
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, unix.ESRCH) {
			continue
		}
		if err != nil {
			p = newProcessInfo(int(record.Proc.P_pid))
			p.Coverage["identity"] = processStatus(err)
		}
		result = append(result, p)
	}
	return result, status, nil
}
func platformProcess(ctx context.Context, pid int) (ProcessInfo, error) {
	p := newProcessInfo(pid)
	if err := ctx.Err(); err != nil {
		return p, err
	}
	record, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return p, err
	}
	if record.Proc.P_pid != int32(pid) {
		return p, os.ErrNotExist
	}
	p.ParentPID = int(record.Eproc.Ppid)
	p.Name = unix.ByteSliceToString(record.Proc.P_comm[:])
	p.Owner = strconv.FormatUint(uint64(record.Eproc.Ucred.Uid), 10)
	start := record.Proc.P_starttime
	if start.Sec != 0 {
		p.StartID = fmt.Sprintf("%d:%d", start.Sec, start.Usec)
		p.Coverage["identity"] = completeProcessStatus()
	} else {
		p.Coverage["identity"] = unsupportedProcessStatus("start time unavailable")
	}
	p.Coverage["owner"] = completeProcessStatus()
	// KERN_PROCARGS2 starts with argc then the executable path. Arguments and
	// environment are not parsed, returned, or persisted.
	data, err := unix.SysctlRaw("kern.procargs2", pid)
	if err == nil && len(data) > 4 {
		if end := bytes.IndexByte(data[4:], 0); end >= 0 {
			p.Executable = string(data[4 : 4+end])
		} else {
			err = errors.New("missing executable path terminator")
		}
	} else if err == nil {
		err = errors.New("executable path unavailable")
	}
	p.Coverage["executable"] = processStatus(err)
	if filepath.IsAbs(p.Executable) {
		components := strings.Split(p.Executable, string(os.PathSeparator))
		for i, part := range components {
			if strings.HasSuffix(strings.ToLower(part), ".app") {
				bundle := strings.Join(components[:i+1], string(os.PathSeparator))
				p.Application = &ApplicationInfo{Name: strings.TrimSuffix(part, filepath.Ext(part)), Path: bundle}
				break
			}
		}
	}
	after, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return p, err
	}
	if after.Proc.P_pid != int32(pid) || after.Proc.P_starttime != start {
		return p, errors.New("process identity changed during discovery")
	}
	return p, nil
}

// A bounded writer also causes os/exec's copy loop to stop if a utility emits
// excessive data. WaitDelay bounds inherited pipes after context cancellation.
type processOutput struct {
	buffer bytes.Buffer
	limit  int
}

func (b *processOutput) Len() int       { return b.buffer.Len() }
func (b *processOutput) Bytes() []byte  { return b.buffer.Bytes() }
func (b *processOutput) String() string { return b.buffer.String() }

func (b *processOutput) Write(data []byte) (int, error) {
	if len(data) > b.limit-b.Len() {
		n := b.limit - b.Len()
		if n > 0 {
			_, _ = b.buffer.Write(data[:n])
		}
		return n, errors.New("process utility output limit reached")
	}
	return b.buffer.Write(data)
}

func platformProcessResources(ctx context.Context, p *ProcessInfo, limit int) {
	darwinProcessUsage(ctx, p)
	for _, kind := range []string{"file", "mapping", "socket", "ipc"} {
		p.Coverage[kind] = completeProcessStatus()
	}
	p.Coverage["ipc"] = CollectionStatus{State: "partial", Detail: "descriptor-based IPC only; Mach port tables are not enumerated"}
	cmd := exec.CommandContext(ctx, "/usr/sbin/lsof", "-nP", "-a", "-p", strconv.Itoa(p.PID), "-F0pftPnT")
	cmd.WaitDelay = processCommandWait
	output := &processOutput{limit: 8 << 20}
	diagnostics := &processOutput{limit: 65536}
	cmd.Stdout = output
	cmd.Stderr = diagnostics
	err := cmd.Run()
	if err != nil || diagnostics.Len() > 0 {
		status := processStatus(err)
		if err == nil || output.Len() > 0 {
			status = CollectionStatus{State: "partial", Detail: "lsof could not inspect every resource"}
		}
		for _, kind := range []string{"file", "mapping", "socket", "ipc"} {
			p.Coverage[kind] = status
		}
	}
	parseDarwinProcessFiles(p, output.Bytes(), limit)
}

func parseDarwinProcessFiles(p *ProcessInfo, data []byte, limit int) {
	fd, typ, name, protocol, state := "", "", "", "", ""
	flush := func() {
		if fd == "" {
			return
		}
		if fd == "NOFD" {
			for _, kind := range []string{"file", "mapping", "socket", "ipc"} {
				p.Coverage[kind] = CollectionStatus{State: "partial", Detail: "lsof could not inspect process descriptors"}
			}
			return
		}
		r := ProcessResource{Descriptor: fd, Path: name}
		switch {
		case typ == "IPv4" || typ == "IPv6" || typ == "unix":
			r.Kind = "socket"
			r.Path = ""
			r.Protocol = protocol
			r.State = state
			if typ == "unix" {
				r.Protocol = "unix"
				r.Endpoint = name
			} else if left, right, ok := strings.Cut(name, "->"); ok {
				r.LocalAddress = left
				r.RemoteAddress = right
			} else {
				r.LocalAddress = name
			}
		case fd == "txt" || fd == "mem":
			r.Kind = "mapping"
		case strings.HasPrefix(name, "/") && (typ == "REG" || typ == "DIR" || typ == "CHR" || typ == "BLK"):
			r.Kind = "file"
		default:
			r.Kind = "ipc"
			r.Path = ""
			r.Endpoint = name
			r.Protocol = typ
		}
		addProcessResource(p, r, limit)
	}
	// -F0 uses NUL field separators and newline record separators. Newlines inside
	// a field value remain part of that value rather than becoming fake records.
	for _, field := range bytes.Split(data, []byte{0}) {
		field = bytes.TrimLeft(field, "\n")
		if len(field) == 0 {
			continue
		}
		value := string(field[1:])
		switch field[0] {
		case 'p':
			flush()
			fd = ""
		case 'f':
			flush()
			fd = value
			typ = ""
			name = ""
			protocol = ""
			state = ""
		case 't':
			typ = value
		case 'n':
			name = value
		case 'P':
			protocol = strings.ToLower(value)
		case 'T':
			if strings.HasPrefix(value, "ST=") {
				state = strings.TrimPrefix(value, "ST=")
			}
		}
	}
	flush()
}

func darwinProcessUsage(ctx context.Context, p *ProcessInfo) {
	cmd := exec.CommandContext(ctx, "/bin/ps", "-p", strconv.Itoa(p.PID), "-o", "rss=,vsz=,time=")
	cmd.WaitDelay = processCommandWait
	output := &processOutput{limit: 65536}
	cmd.Stdout = output
	if err := cmd.Run(); err != nil {
		p.Coverage["usage"] = processStatus(err)
		return
	}
	fields := strings.Fields(output.String())
	if len(fields) != 3 {
		p.Coverage["usage"] = CollectionStatus{State: "unavailable", Detail: "unexpected ps usage record"}
		return
	}
	rss, e1 := strconv.ParseUint(fields[0], 10, 64)
	vms, e2 := strconv.ParseUint(fields[1], 10, 64)
	seconds, e3 := parseProcessCPUTime(fields[2])
	if err := errors.Join(e1, e2, e3); err != nil {
		p.Coverage["usage"] = processStatus(err)
		return
	}
	rss *= 1024
	vms *= 1024
	p.Usage = &ProcessUsage{ResidentBytes: &rss, VirtualBytes: &vms, CPUSeconds: &seconds}
	p.Coverage["usage"] = CollectionStatus{State: "partial", Detail: "thread count not collected"}
}
func parseProcessCPUTime(value string) (float64, error) {
	days := float64(0)
	if d, rest, ok := strings.Cut(value, "-"); ok {
		n, err := strconv.ParseUint(d, 10, 32)
		if err != nil {
			return 0, err
		}
		days = float64(n)
		value = rest
	}
	fields := strings.Split(value, ":")
	if len(fields) < 2 || len(fields) > 3 {
		return 0, errors.New("invalid CPU time")
	}
	total := float64(0)
	for _, field := range fields {
		n, err := strconv.ParseFloat(field, 64)
		if err != nil || n < 0 {
			return 0, errors.New("invalid CPU time")
		}
		total = total*60 + n
	}
	return days*86400 + total, nil
}
