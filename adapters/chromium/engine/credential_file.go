package chromium

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// configuredSafeStoragePassword allows an explicit local key file for headless
// use without placing the secret in a command argument or environment value.
func configuredSafeStoragePassword(provider Config) (string, bool, error) {
	name := strings.ToUpper(strings.ReplaceAll(provider.Name, "-", "_"))
	if name == "" {
		return "", false, errors.New("Chromium adapter name is empty")
	}
	for _, character := range name {
		if character != '_' && (character < 'A' || character > 'Z') && (character < '0' || character > '9') {
			return "", false, errors.New("invalid Chromium adapter name for credential file")
		}
	}
	variable := "EXT_BROWSER_" + name + "_SAFE_STORAGE_PASSWORD_FILE"
	path := os.Getenv(variable)
	if path == "" {
		return "", false, nil
	}
	if !filepath.IsAbs(path) {
		return "", true, fmt.Errorf("%s must be an absolute path", variable)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return "", true, fmt.Errorf("read %s: %w", variable, err)
	}
	if !privateCredentialFile(info) {
		return "", true, fmt.Errorf("%s must name a private regular file of 1–4096 bytes", variable)
	}
	file, err := os.Open(path)
	if err != nil {
		return "", true, fmt.Errorf("open %s: %w", variable, err)
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) || !privateCredentialFile(opened) {
		return "", true, fmt.Errorf("%s changed or is not a private regular file", variable)
	}
	data, err := io.ReadAll(io.LimitReader(file, 4097))
	if err != nil || len(data) > 4096 {
		return "", true, fmt.Errorf("read %s: invalid password file", variable)
	}
	password := strings.TrimSuffix(strings.TrimSuffix(string(data), "\n"), "\r")
	if password == "" || strings.ContainsAny(password, "\r\n\x00") {
		return "", true, fmt.Errorf("%s contains an empty or invalid password", variable)
	}
	return password, true, nil
}

func privateCredentialFile(info os.FileInfo) bool {
	return info.Mode().IsRegular() && (info.Mode().Perm() == 0o600 || info.Mode().Perm() == 0o400) && info.Size() > 0 && info.Size() <= 4096
}
