//go:build windows

package store

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"syscall"
	"unsafe"

	"github.com/webong/ext/res/credential/adapterkit"
)

const genericCredential = 1
const persistLocalMachine = 2
const maxCredentialBlob = 5 * 512
const errorNotFound syscall.Errno = 1168

var credentialDLL = syscall.NewLazyDLL("advapi32.dll")
var credRead = credentialDLL.NewProc("CredReadW")
var credWrite = credentialDLL.NewProc("CredWriteW")
var credFree = credentialDLL.NewProc("CredFree")

type nativeCredential struct {
	Flags              uint32
	Type               uint32
	TargetName         *uint16
	Comment            *uint16
	LastWritten        syscall.Filetime
	CredentialBlobSize uint32
	CredentialBlob     *byte
	Persist            uint32
	AttributeCount     uint32
	Attributes         uintptr
	TargetAlias        *uint16
	UserName           *uint16
}

func (CredentialManager) Check(context.Context) error {
	if err := credRead.Find(); err != nil {
		return errors.New("Windows Credential Manager is unavailable")
	}
	return nil
}

func (CredentialManager) Get(_ context.Context, item string) ([]byte, error) {
	target, err := parseItem(item)
	if err != nil {
		return nil, err
	}
	return readTarget(target)
}

func readTarget(target string) ([]byte, error) {
	name, err := syscall.UTF16PtrFromString(target)
	if err != nil {
		return nil, errors.New("invalid Credential Manager target")
	}
	var pointer *nativeCredential
	ok, _, callErr := credRead.Call(uintptr(unsafe.Pointer(name)), genericCredential, 0, uintptr(unsafe.Pointer(&pointer)))
	runtime.KeepAlive(name)
	if ok == 0 {
		if errors.Is(callErr, errorNotFound) {
			return nil, errorNotFound
		}
		return nil, fmt.Errorf("Credential Manager read failed: %w", callErr)
	}
	defer credFree.Call(uintptr(unsafe.Pointer(pointer)))
	if pointer.CredentialBlobSize == 0 || pointer.CredentialBlobSize > 1024*1024 || pointer.CredentialBlob == nil {
		return nil, errors.New("Credential Manager item is empty or too large")
	}
	return append([]byte(nil), unsafe.Slice(pointer.CredentialBlob, pointer.CredentialBlobSize)...), nil
}

func (CredentialManager) Put(_ context.Context, item string, value []byte, replace bool) error {
	if len(value) == 0 || len(value) > maxCredentialBlob {
		return fmt.Errorf("Credential Manager generic item must contain 1 to %d bytes", maxCredentialBlob)
	}
	target, err := parseItem(item)
	if err != nil {
		return err
	}
	if !replace {
		if _, err := readTarget(target); err == nil {
			return adapterkit.ErrExists
		} else if !errors.Is(err, errorNotFound) {
			return err
		}
	}
	name, err := syscall.UTF16PtrFromString(target)
	if err != nil {
		return errors.New("invalid Credential Manager target")
	}
	entry := nativeCredential{
		Type: genericCredential, TargetName: name,
		CredentialBlobSize: uint32(len(value)), CredentialBlob: &value[0],
		Persist: persistLocalMachine,
	}
	ok, _, callErr := credWrite.Call(uintptr(unsafe.Pointer(&entry)), 0)
	runtime.KeepAlive(name)
	runtime.KeepAlive(value)
	if ok == 0 {
		return fmt.Errorf("Credential Manager write failed: %w", callErr)
	}
	return nil
}
