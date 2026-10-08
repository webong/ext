//go:build windows

package systemgraph

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"syscall"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

// These layouts follow processsnapshot.h. The handle record is the header's size
// exactly (136 bytes on 64-bit): PssWalkSnapshot rejects a larger buffer with
// ERROR_BAD_LENGTH on current Windows images, as the native CI run showed.
type processHandleEntry struct {
	Handle     uintptr
	Flags      uint32
	ObjectType uint32
	Capture    windows.Filetime
	Attributes uint32
	Access     uint32
	Handles    uint32
	Pointers   uint32
	Paged      uint32
	NonPaged   uint32
	Created    windows.Filetime
	TypeLength uint16
	TypeName   *uint16
	NameLength uint16
	Name       *uint16
	Union      [48]byte // the largest member of TypeSpecificInformation (Thread)
}
type processVAEntry struct {
	Base                 uintptr
	Allocation           uintptr
	AllocationProtection uint32
	RegionSize           uintptr
	State                uint32
	Protection           uint32
	Type                 uint32
	ImageTime            uint32
	ImageSize            uint32
	ImageBase            uintptr
	Checksum             uint32
	NameLength           uint16
	Name                 *uint16
}

type processSnapshotAPI struct {
	capture, free, createMarker, freeMarker, walk *windows.LazyProc
}

func loadProcessSnapshotAPI() (*processSnapshotAPI, error) {
	dll := windows.NewLazySystemDLL("kernel32.dll")
	api := &processSnapshotAPI{dll.NewProc("PssCaptureSnapshot"), dll.NewProc("PssFreeSnapshot"), dll.NewProc("PssWalkMarkerCreate"), dll.NewProc("PssWalkMarkerFree"), dll.NewProc("PssWalkSnapshot")}
	for _, proc := range []*windows.LazyProc{api.capture, api.free, api.createMarker, api.freeMarker, api.walk} {
		if err := proc.Find(); err != nil {
			return nil, err
		}
	}
	return api, nil
}
func windowsPSSStatus(err error) CollectionStatus {
	if errors.Is(err, windows.ERROR_NOT_SUPPORTED) || errors.Is(err, windows.ERROR_PROC_NOT_FOUND) {
		return unsupportedProcessStatus("Windows process snapshotting unavailable")
	}
	return processStatus(err)
}
func (api *processSnapshotAPI) withSnapshot(pid int, access uint32, flags uintptr, walk func(uintptr) error) error {
	h, err := windows.OpenProcess(access, false, uint32(pid))
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	var snapshot uintptr
	result, _, _ := api.capture.Call(uintptr(h), flags, 0, uintptr(unsafe.Pointer(&snapshot)))
	if result != 0 {
		return syscall.Errno(result)
	}
	// The snapshot is owned by the calling process, irrespective of its target.
	defer api.free.Call(uintptr(windows.CurrentProcess()), snapshot)
	return walk(snapshot)
}
func (api *processSnapshotAPI) walkRecords(ctx context.Context, snapshot, class uintptr, limit int, record unsafe.Pointer, size uintptr, visit func()) error {
	var marker uintptr
	result, _, _ := api.createMarker.Call(0, uintptr(unsafe.Pointer(&marker)))
	if result != 0 {
		return syscall.Errno(result)
	}
	defer api.freeMarker.Call(marker)
	for count := 0; ; count++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		clear(unsafe.Slice((*byte)(record), int(size)))
		result, _, _ := api.walk.Call(snapshot, class, marker, uintptr(record), size)
		if syscall.Errno(result) == windows.ERROR_NO_MORE_ITEMS {
			return nil
		}
		if result != 0 {
			return syscall.Errno(result)
		}
		if count >= limit {
			return errProcessRecordLimit
		}
		visit()
	}
}

var errProcessRecordLimit = errors.New("record limit reached")

func pssString(pointer *uint16, bytes uint16) string {
	if pointer == nil || bytes == 0 {
		return ""
	}
	// The reported length includes the terminating NUL, which would make "File" compare unequal.
	return strings.TrimRight(string(utf16.Decode(unsafe.Slice(pointer, int(bytes)/2))), "\x00")
}

// The capture flags are PSS_CAPTURE_HANDLES (0x4), PSS_CAPTURE_HANDLE_NAME_INFORMATION (0x8) and
// PSS_CAPTURE_HANDLE_BASIC_INFORMATION (0x10). Without the last, every handle comes back without
// a type name, so no handle can be classed as a file.
func windowsProcessHandles(ctx context.Context, p *ProcessInfo, limit int) {
	api, err := loadProcessSnapshotAPI()
	if err != nil {
		p.Coverage["file"] = windowsPSSStatus(err)
		p.Coverage["ipc"] = windowsPSSStatus(err)
		return
	}
	p.Coverage["file"] = completeProcessStatus()
	p.Coverage["ipc"] = completeProcessStatus()
	// The snapshot does not name every file handle. For those, duplicate the handle into this
	// process and ask for its final path; this needs the same PROCESS_DUP_HANDLE right, and
	// when it is denied the handle simply stays unnamed and the coverage says so.
	owner, ownerErr := windows.OpenProcess(windows.PROCESS_DUP_HANDLE, false, uint32(p.PID))
	if ownerErr == nil {
		defer windows.CloseHandle(owner)
	}
	err = api.withSnapshot(p.PID, windows.PROCESS_QUERY_INFORMATION|windows.PROCESS_DUP_HANDLE, 0x4|0x8|0x10, func(snapshot uintptr) error {
		entry := processHandleEntry{}
		return api.walkRecords(ctx, snapshot, 2, limit*4, unsafe.Pointer(&entry), unsafe.Sizeof(entry), func() {
			typ, name := pssString(entry.TypeName, entry.TypeLength), pssString(entry.Name, entry.NameLength)
			if entry.Flags&2 == 0 {
				name = ""
			}
			r := ProcessResource{Descriptor: strconv.FormatUint(uint64(entry.Handle), 16), Protocol: typ}
			pipe := false
			if name == "" && ownerErr == nil && strings.EqualFold(typ, "File") {
				name, pipe = windowsFileHandleDetails(owner, entry.Handle)
			}
			if pipe {
				// The system does not name a pipe handle safely, but it is IPC, not a file.
				r.Kind = "ipc"
			} else if strings.EqualFold(typ, "File") && !strings.HasPrefix(strings.ToLower(name), `\device\namedpipe\`) {
				r.Kind = "file"
				r.Path = name
				if name == "" {
					p.Coverage["file"] = CollectionStatus{State: "partial", Detail: "some file handles have no available name"}
				}
			} else {
				r.Kind = "ipc"
				r.Endpoint = name
				if typ == "" {
					p.Coverage["file"] = CollectionStatus{State: "partial", Detail: "some handle types are unavailable"}
					p.Coverage["ipc"] = CollectionStatus{State: "partial", Detail: "some handle types are unavailable"}
				}
			}
			addProcessResource(p, r, limit)
		})
	})
	if err != nil {
		status := windowsPSSStatus(err)
		if errors.Is(err, errProcessRecordLimit) {
			status = limitedProcessStatus()
		}
		for _, kind := range []string{"file", "ipc"} {
			p.Coverage[kind] = status
		}
	}
}

// windowsFileHandleDetails describes a File-type handle of another process that the snapshot did
// not name. It duplicates the handle into this process and asks what it is: GetFileType never
// blocks, and a pipe is reported as such without a name, because asking a pipe for its path can
// block. For a disk file it reads the final path in NT form, like the paths the snapshot reports.
// A denied duplication returns "", false and the handle stays an unnamed file.
func windowsFileHandleDetails(owner windows.Handle, handle uintptr) (path string, pipe bool) {
	var duplicate windows.Handle
	if err := windows.DuplicateHandle(owner, windows.Handle(handle), windows.CurrentProcess(), &duplicate, 0, false, windows.DUPLICATE_SAME_ACCESS); err != nil {
		return "", false
	}
	defer windows.CloseHandle(duplicate)
	kind, err := windows.GetFileType(duplicate)
	if err != nil {
		return "", false
	}
	if kind == windows.FILE_TYPE_PIPE {
		return "", true
	}
	if kind != windows.FILE_TYPE_DISK {
		return "", false
	}
	const volumeNameNT = 0x2 // VOLUME_NAME_NT
	buffer := make([]uint16, 1024)
	length, err := windows.GetFinalPathNameByHandle(duplicate, &buffer[0], uint32(len(buffer)), volumeNameNT)
	if err != nil || length == 0 || int(length) >= len(buffer) {
		return "", false
	}
	return windows.UTF16ToString(buffer[:length]), false
}

// windowsProcessMappings augments module enumeration with named memory mappings.
// No VA clone, memory-page capture, or thread-context capture is requested.
func windowsProcessMappings(ctx context.Context, p *ProcessInfo, limit int) {
	api, err := loadProcessSnapshotAPI()
	if err != nil {
		return
	} // Module coverage remains explicit.
	paths := map[string]bool{}
	for _, r := range p.Resources {
		if r.Kind == "mapping" {
			paths[strings.ToLower(r.Path)] = true
		}
	}
	status := completeProcessStatus()
	err = api.withSnapshot(p.PID, windows.PROCESS_QUERY_INFORMATION|windows.PROCESS_VM_READ, 0x800|0x1000, func(snapshot uintptr) error {
		entry := processVAEntry{}
		return api.walkRecords(ctx, snapshot, 1, limit*16, unsafe.Pointer(&entry), unsafe.Sizeof(entry), func() {
			// MEM_MAPPED and MEM_IMAGE are the file-backed region classes.
			if entry.Type != 0x40000 && entry.Type != 0x1000000 {
				return
			}
			name := pssString(entry.Name, entry.NameLength)
			if name == "" {
				status = CollectionStatus{State: "partial", Detail: "some mappings have no available backing path"}
				return
			}
			key := strings.ToLower(name)
			if paths[key] {
				return
			}
			paths[key] = true
			addProcessResource(p, ProcessResource{Kind: "mapping", Path: name}, limit)
		})
	})
	if err != nil {
		if errors.Is(err, errProcessRecordLimit) {
			p.Coverage["mapping"] = limitedProcessStatus()
		}
		return
	}
	if p.Coverage["mapping"].Detail != "record limit reached" && p.Coverage["mapping"].Detail != "resource exceeds graph string limit" {
		p.Coverage["mapping"] = status
	}
}
