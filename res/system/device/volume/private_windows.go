//go:build windows

package volume

import (
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// isReparsePoint detects junctions, mount points, and other reparse points that
// os.FileInfo may present as ordinary directories.
func isReparsePoint(path string, info os.FileInfo) bool {
	if info.Mode()&os.ModeSymlink != 0 {
		return true
	}
	pointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return true
	}
	attributes, err := windows.GetFileAttributes(pointer)
	if err != nil {
		return false
	}
	return attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0
}

// isMountRoot is unnecessary on Windows: drive roots are rejected as broad
// targets and a mounted folder is caught as a reparse point.
func isMountRoot(string) bool { return false }

// writeAccessMask covers data, metadata, deletion, and permission mutation.
const writeAccessMask = windows.GENERIC_WRITE | windows.GENERIC_ALL |
	windows.FILE_WRITE_DATA | windows.FILE_APPEND_DATA | windows.FILE_WRITE_EA |
	windows.FILE_WRITE_ATTRIBUTES | windows.DELETE | windows.WRITE_DAC | windows.WRITE_OWNER

// verifyPrivatePermissions refuses a directory whose DACL grants write access to
// Everyone, Authenticated Users, the local Users group, Guests, or Anonymous.
// Ownership and administrator-only access are left to the caller.
func verifyPrivatePermissions(path string, _ os.FileInfo) error {
	pointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return fmt.Errorf("%w: private volume path is invalid", ErrUnsafePath)
	}
	handle, err := windows.CreateFile(pointer, windows.READ_CONTROL,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return fmt.Errorf("%w: private volume security is unreadable", ErrUnsafePath)
	}
	defer windows.CloseHandle(handle)
	descriptor, err := windows.GetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("%w: private volume security is unreadable", ErrUnsafePath)
	}
	acl, _, err := descriptor.DACL()
	if err != nil {
		return fmt.Errorf("%w: private volume security is unreadable", ErrUnsafePath)
	}
	if acl == nil {
		return fmt.Errorf("%w: private volume has no discretionary access control", ErrUnsafePath)
	}
	for index := uint32(0); index < uint32(acl.AceCount); index++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(acl, index, &ace); err != nil {
			return fmt.Errorf("%w: private volume access entry is unreadable", ErrUnsafePath)
		}
		switch ace.Header.AceType {
		case windows.ACCESS_ALLOWED_ACE_TYPE:
			// Check below.
		case windows.ACCESS_DENIED_ACE_TYPE:
			continue
		default:
			return fmt.Errorf("%w: private volume uses an access entry this check cannot classify", ErrUnsafePath)
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if !broadWriteSID(sid.String()) {
			continue
		}
		if ace.Mask&writeAccessMask != 0 {
			return fmt.Errorf("%w: private volume is writable by %s", ErrUnsafePath, sid.String())
		}
	}
	return nil
}
