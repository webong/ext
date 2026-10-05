//go:build darwin && cgo

package store

/*
#cgo LDFLAGS: -framework Security -framework CoreFoundation
#include <Security/Security.h>
#include <stdlib.h>
#include <string.h>

static OSStatus ctx_keychain_open(const char *path, SecKeychainRef *keychain) {
    if (path == NULL) return SecKeychainCopyDefault(keychain);
    return SecKeychainOpen(path, keychain);
}

static OSStatus ctx_keychain_check(const char *path) {
    SecKeychainRef keychain = NULL;
    OSStatus status = ctx_keychain_open(path, &keychain);
    if (keychain != NULL) CFRelease(keychain);
    return status;
}

static OSStatus ctx_keychain_read(const char *path, const char *service, const char *account, void **out, UInt32 *out_len) {
    SecKeychainRef keychain = NULL;
    OSStatus status = ctx_keychain_open(path, &keychain);
    if (status != errSecSuccess) return status;
    void *data = NULL;
    UInt32 length = 0;
    status = SecKeychainFindGenericPassword(keychain,
        (UInt32)strlen(service), service, (UInt32)strlen(account), account,
        &length, &data, NULL);
    if (keychain != NULL) CFRelease(keychain);
    if (status != errSecSuccess) return status;
    if (length > 1024 * 1024) {
        SecKeychainItemFreeContent(NULL, data);
        return errSecParam;
    }
    void *copy = malloc(length == 0 ? 1 : length);
    if (copy == NULL) {
        SecKeychainItemFreeContent(NULL, data);
        return errSecAllocate;
    }
    if (length != 0) memcpy(copy, data, length);
    SecKeychainItemFreeContent(NULL, data);
    *out = copy;
    *out_len = length;
    return errSecSuccess;
}

static OSStatus ctx_keychain_write(const char *path, const char *service, const char *account,
    const void *value, UInt32 length, int replace, int *exists) {
    SecKeychainRef keychain = NULL;
    OSStatus status = ctx_keychain_open(path, &keychain);
    if (status != errSecSuccess) return status;
    SecKeychainItemRef item = NULL;
    status = SecKeychainFindGenericPassword(keychain,
        (UInt32)strlen(service), service, (UInt32)strlen(account), account,
        NULL, NULL, &item);
    if (status == errSecSuccess) {
        if (!replace) {
            *exists = 1;
            CFRelease(item);
            if (keychain != NULL) CFRelease(keychain);
            return errSecSuccess;
        }
        status = SecKeychainItemModifyAttributesAndData(item, NULL, length, value);
        CFRelease(item);
        if (keychain != NULL) CFRelease(keychain);
        return status;
    }
    if (status != errSecItemNotFound) {
        if (keychain != NULL) CFRelease(keychain);
        return status;
    }
    status = SecKeychainAddGenericPassword(keychain,
        (UInt32)strlen(service), service, (UInt32)strlen(account), account,
        length, value, NULL);
    if (keychain != NULL) CFRelease(keychain);
    return status;
}
*/
import "C"

import (
	"context"
	"fmt"
	"path/filepath"
	"unsafe"

	"github.com/webong/ctx/res/credential/adapterkit"
)

func (keychain Keychain) Check(context.Context) error {
	path, cleanup, err := keychain.cPath()
	if err != nil {
		return err
	}
	defer cleanup()
	if status := C.ctx_keychain_check(path); status != C.errSecSuccess {
		return fmt.Errorf("default Keychain unavailable (OSStatus %d)", int(status))
	}
	return nil
}

func (keychain Keychain) Get(_ context.Context, item string) ([]byte, error) {
	service, account, err := parseItem(item)
	if err != nil {
		return nil, err
	}
	name, identity := C.CString(service), C.CString(account)
	defer C.free(unsafe.Pointer(name))
	defer C.free(unsafe.Pointer(identity))
	path, cleanup, err := keychain.cPath()
	if err != nil {
		return nil, err
	}
	defer cleanup()
	var value unsafe.Pointer
	var length C.UInt32
	status := C.ctx_keychain_read(path, name, identity, &value, &length)
	if status != C.errSecSuccess {
		return nil, fmt.Errorf("Keychain read denied or item missing (OSStatus %d)", int(status))
	}
	defer func() {
		C.memset(value, 0, C.size_t(length))
		C.free(value)
	}()
	return C.GoBytes(value, C.int(length)), nil
}

func (keychain Keychain) Put(_ context.Context, item string, value []byte, replace bool) error {
	if len(value) == 0 || len(value) > 1024*1024 {
		return fmt.Errorf("Keychain value must contain 1 byte to 1 MiB")
	}
	service, account, err := parseItem(item)
	if err != nil {
		return err
	}
	name, identity := C.CString(service), C.CString(account)
	defer C.free(unsafe.Pointer(name))
	defer C.free(unsafe.Pointer(identity))
	path, cleanup, err := keychain.cPath()
	if err != nil {
		return err
	}
	defer cleanup()
	secret := C.CBytes(value)
	defer func() {
		C.memset(secret, 0, C.size_t(len(value)))
		C.free(secret)
	}()
	var exists C.int
	overwrite := C.int(0)
	if replace {
		overwrite = 1
	}
	status := C.ctx_keychain_write(path, name, identity, secret, C.UInt32(len(value)), overwrite, &exists)
	if exists != 0 {
		return adapterkit.ErrExists
	}
	if status != C.errSecSuccess {
		return fmt.Errorf("Keychain write denied (OSStatus %d)", int(status))
	}
	return nil
}

func (keychain Keychain) cPath() (*C.char, func(), error) {
	if keychain.Path == "" {
		return nil, func() {}, nil
	}
	if !filepath.IsAbs(keychain.Path) {
		return nil, nil, fmt.Errorf("macOS keychain path must be absolute")
	}
	path := C.CString(keychain.Path)
	return path, func() { C.free(unsafe.Pointer(path)) }, nil
}
