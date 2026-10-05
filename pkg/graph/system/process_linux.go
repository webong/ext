//go:build linux

package systemgraph

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unsafe"
)

func processHostID(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return "", err
	}
	pidNS, err := os.Readlink("/proc/self/ns/pid")
	if err != nil {
		return "", err
	}
	netNS, err := os.Readlink("/proc/self/ns/net")
	if err != nil {
		return "", err
	}
	return digest(hostEnvironment() + string(boot) + pidNS + netNS), nil
}

func platformProcesses(ctx context.Context, limit int) ([]ProcessInfo, CollectionStatus, error) {
	dir, err := os.Open("/proc")
	if err != nil {
		return nil, CollectionStatus{}, err
	}
	defer dir.Close()
	result := []ProcessInfo{}
	status := completeProcessStatus()
	for {
		names, err := dir.Readdirnames(256)
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, status, err
		}
		for _, name := range names {
			if err := ctx.Err(); err != nil {
				return nil, status, err
			}
			pid, e := strconv.Atoi(name)
			if e != nil || pid <= 0 {
				continue
			}
			if len(result) >= limit {
				return result, limitedProcessStatus(), nil
			}
			p, e := platformProcess(ctx, pid)
			if errors.Is(e, os.ErrNotExist) {
				continue
			}
			if e != nil {
				p = newProcessInfo(pid)
				p.Coverage["identity"] = processStatus(e)
			}
			result = append(result, p)
		}
		if errors.Is(err, io.EOF) {
			break
		}
	}
	return result, status, nil
}

// readProcessFile bounds proc pseudo-file allocation even while a process changes.
func readProcessFile(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("process data exceeds %d bytes", limit)
	}
	return data, nil
}

func platformProcess(ctx context.Context, pid int) (ProcessInfo, error) {
	p := newProcessInfo(pid)
	if err := ctx.Err(); err != nil {
		return p, err
	}
	root := "/proc/" + strconv.Itoa(pid)
	data, err := readProcessFile(root+"/stat", 65536)
	if err != nil {
		return p, err
	}
	// comm may contain spaces, parentheses, and newlines. Fields after its final
	// closing parenthesis have fixed positions in the kernel proc contract.
	text := string(data)
	open, close := strings.IndexByte(text, '('), strings.LastIndexByte(text, ')')
	if open < 0 || close <= open {
		return p, errors.New("invalid process stat record")
	}
	fields := strings.Fields(text[close+1:])
	if len(fields) < 22 {
		return p, errors.New("short process stat record")
	}
	p.Name = text[open+1 : close]
	p.ParentPID, err = strconv.Atoi(fields[1])
	if err != nil {
		return p, err
	}
	start, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil {
		return p, err
	}
	p.StartID = strconv.FormatUint(start, 10)
	p.Coverage["identity"] = completeProcessStatus()
	p.Executable, err = os.Readlink(root + "/exe")
	p.Coverage["executable"] = processStatus(err)
	data, err = readProcessFile(root+"/status", 1<<20)
	if err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "Uid:") {
				ids := strings.Fields(line)
				if len(ids) > 2 {
					p.Owner = ids[2]
				}
			}
		}
		if p.Owner == "" {
			err = errors.New("effective UID missing from process status")
		}
	}
	p.Coverage["owner"] = processStatus(err)
	// Verify fields were not attributed to a reused PID during the path reads.
	last, e := readProcessFile(root+"/stat", 65536)
	if e != nil {
		return p, e
	}
	end := strings.LastIndexByte(string(last), ')')
	if end < 0 {
		return p, errors.New("invalid process stat recheck")
	}
	tail := strings.Fields(string(last[end+1:]))
	if len(tail) < 20 || tail[19] != p.StartID {
		return p, errors.New("process identity changed during discovery")
	}
	return p, nil
}

func platformProcessResources(ctx context.Context, p *ProcessInfo, limit int) {
	root := "/proc/" + strconv.Itoa(p.PID)
	linuxProcessUsage(p, root)
	p.Coverage["file"] = completeProcessStatus()
	p.Coverage["socket"] = completeProcessStatus()
	p.Coverage["ipc"] = completeProcessStatus()
	entries, err := os.Open(root + "/fd")
	socketFDs := map[string][]string{}
	if err != nil {
		for _, kind := range []string{"file", "socket", "ipc"} {
			p.Coverage[kind] = processStatus(err)
		}
	} else {
		defer entries.Close()
		seen := 0
		for {
			names, e := entries.Readdirnames(128)
			if e != nil && !errors.Is(e, io.EOF) {
				for _, kind := range []string{"file", "socket", "ipc"} {
					p.Coverage[kind] = processStatus(e)
				}
				break
			}
			for _, fd := range names {
				if ctx.Err() != nil {
					return
				}
				seen++
				if seen > limit*4 {
					for _, kind := range []string{"file", "socket", "ipc"} {
						p.Coverage[kind] = limitedProcessStatus()
					}
					break
				}
				path, e := os.Readlink(root + "/fd/" + fd)
				if e != nil {
					for _, kind := range []string{"file", "socket", "ipc"} {
						p.Coverage[kind] = CollectionStatus{State: "partial", Detail: "some descriptors changed or were inaccessible"}
					}
					continue
				}
				switch {
				case strings.HasPrefix(path, "socket:["):
					inode := strings.TrimSuffix(strings.TrimPrefix(path, "socket:["), "]")
					socketFDs[inode] = append(socketFDs[inode], fd)
				case strings.HasPrefix(path, "/"):
					addProcessResource(p, ProcessResource{Kind: "file", Path: path, Descriptor: fd}, limit)
				default:
					addProcessResource(p, ProcessResource{Kind: "ipc", Endpoint: path, Descriptor: fd}, limit)
				}
			}
			if seen > limit*4 || errors.Is(e, io.EOF) {
				break
			}
		}
		if len(socketFDs) > 0 {
			linuxProcessSockets(ctx, p, root, socketFDs, limit)
		}
	}
	p.Coverage["mapping"] = completeProcessStatus()
	data, err := readProcessFile(root+"/maps", 8<<20)
	if err != nil {
		p.Coverage["mapping"] = processStatus(err)
		return
	}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 4096), 1<<20)
	paths := map[string]bool{}
	for scanner.Scan() {
		if ctx.Err() != nil {
			return
		}
		// The first five fields precede the pathname, whose spaces must be preserved.
		path := processFieldsRemainder(scanner.Text(), 5)
		if !filepath.IsAbs(path) || paths[path] {
			continue
		}
		paths[path] = true
		addProcessResource(p, ProcessResource{Kind: "mapping", Path: path}, limit)
	}
	if err := scanner.Err(); err != nil {
		p.Coverage["mapping"] = processStatus(err)
	}
}

func linuxProcessUsage(p *ProcessInfo, root string) {
	data, err := readProcessFile(root+"/stat", 65536)
	if err != nil {
		p.Coverage["usage"] = processStatus(err)
		return
	}
	end := strings.LastIndexByte(string(data), ')')
	if end < 0 {
		p.Coverage["usage"] = CollectionStatus{State: "unavailable", Detail: "invalid process stat"}
		return
	}
	f := strings.Fields(string(data[end+1:]))
	if len(f) < 22 {
		p.Coverage["usage"] = CollectionStatus{State: "unavailable", Detail: "short process stat"}
		return
	}
	threads, e1 := strconv.ParseUint(f[17], 10, 32)
	vms, e2 := strconv.ParseUint(f[20], 10, 64)
	rss, e3 := strconv.ParseUint(f[21], 10, 64)
	if err := errors.Join(e1, e2, e3); err != nil {
		p.Coverage["usage"] = processStatus(err)
		return
	}
	rss *= uint64(os.Getpagesize())
	t := uint32(threads)
	p.Usage = &ProcessUsage{ResidentBytes: &rss, VirtualBytes: &vms, Threads: &t}
	p.Coverage["usage"] = completeProcessStatus()
	// AT_CLKTCK supplies the real userspace clock tick frequency; do not guess HZ.
	aux, err := readProcessFile("/proc/self/auxv", 65536)
	word := int(unsafe.Sizeof(uintptr(0)))
	hz := uint64(0)
	if err == nil {
		for i := 0; i+2*word <= len(aux); i += 2 * word {
			var k, v uint64
			if word == 8 {
				k = binary.NativeEndian.Uint64(aux[i:])
				v = binary.NativeEndian.Uint64(aux[i+word:])
			} else {
				k = uint64(binary.NativeEndian.Uint32(aux[i:]))
				v = uint64(binary.NativeEndian.Uint32(aux[i+word:]))
			}
			if k == 17 {
				hz = v
				break
			}
		}
	}
	u, e1 := strconv.ParseUint(f[11], 10, 64)
	s, e2 := strconv.ParseUint(f[12], 10, 64)
	if hz > 0 && e1 == nil && e2 == nil {
		seconds := (float64(u) + float64(s)) / float64(hz)
		p.Usage.CPUSeconds = &seconds
	} else {
		p.Coverage["usage"] = CollectionStatus{State: "partial", Detail: "CPU clock frequency or counters unavailable"}
	}
}

func linuxProcessSockets(ctx context.Context, p *ProcessInfo, root string, fds map[string][]string, limit int) {
	matched := map[string]bool{}
	for _, table := range []string{"tcp", "tcp6", "udp", "udp6", "unix"} {
		if ctx.Err() != nil {
			return
		}
		data, err := readProcessFile(root+"/net/"+table, 8<<20)
		if err != nil {
			p.Coverage["socket"] = CollectionStatus{State: "partial", Detail: "some socket tables unavailable"}
			continue
		}
		for _, line := range strings.Split(string(data), "\n")[1:] {
			f := strings.Fields(line)
			inode := ""
			r := ProcessResource{Kind: "socket", Protocol: table}
			if table == "unix" {
				if len(f) < 7 {
					continue
				}
				inode = f[6]
				r.State = f[5]
				if len(f) > 7 {
					r.Endpoint = processFieldsRemainder(line, 7)
				}
			} else {
				if len(f) < 10 {
					continue
				}
				inode = f[9]
				if len(fds[inode]) == 0 {
					continue
				}
				local, e1 := linuxSocketAddress(f[1])
				remote, e2 := linuxSocketAddress(f[2])
				if e1 != nil || e2 != nil {
					p.Coverage["socket"] = CollectionStatus{State: "partial", Detail: "invalid socket address"}
					continue
				}
				r.LocalAddress = local
				r.RemoteAddress = remote
				r.State = linuxTCPState(f[3])
				if strings.HasPrefix(table, "udp") {
					r.State = ""
				}
			}
			for _, fd := range fds[inode] {
				r.Descriptor = fd
				addProcessResource(p, r, limit)
				matched[inode] = true
			}
		}
	}
	for inode, descriptors := range fds {
		if matched[inode] {
			continue
		}
		p.Coverage["socket"] = CollectionStatus{State: "partial", Detail: "some socket protocols or endpoints could not be resolved"}
		for _, fd := range descriptors {
			addProcessResource(p, ProcessResource{Kind: "socket", Descriptor: fd, Endpoint: "socket:[" + inode + "]"}, limit)
		}
	}
}

func linuxSocketAddress(value string) (string, error) {
	host, port, ok := strings.Cut(value, ":")
	if !ok || (len(host) != 8 && len(host) != 32) {
		return "", errors.New("invalid socket address")
	}
	ip := make(net.IP, len(host)/2)
	for i := 0; i < len(host); i += 8 {
		word, err := strconv.ParseUint(host[i:i+8], 16, 32)
		if err != nil {
			return "", err
		}
		binary.NativeEndian.PutUint32(ip[i/2:], uint32(word))
	}
	n, err := strconv.ParseUint(port, 16, 16)
	if err != nil {
		return "", err
	}
	return net.JoinHostPort(ip.String(), strconv.FormatUint(n, 10)), nil
}
func linuxTCPState(s string) string {
	switch s {
	case "01":
		return "ESTABLISHED"
	case "02":
		return "SYN_SENT"
	case "03":
		return "SYN_RECV"
	case "04":
		return "FIN_WAIT1"
	case "05":
		return "FIN_WAIT2"
	case "06":
		return "TIME_WAIT"
	case "07":
		return "CLOSE"
	case "08":
		return "CLOSE_WAIT"
	case "09":
		return "LAST_ACK"
	case "0A":
		return "LISTEN"
	case "0B":
		return "CLOSING"
	}
	return s
}

// Preserve internal whitespace in proc paths after their fixed metadata prefix.
func processFieldsRemainder(line string, fields int) string {
	for i := 0; i < fields; i++ {
		line = strings.TrimLeft(line, " \t")
		pos := strings.IndexAny(line, " \t")
		if pos < 0 {
			return ""
		}
		line = line[pos:]
	}
	return strings.TrimLeft(line, " \t")
}
