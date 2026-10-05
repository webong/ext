package chromium

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/webong/ctx/res/browser/extension"
)

var chromiumExtensionID = regexp.MustCompile(`^[a-p]{32}$`)

// StoreConfig describes the store URLs and native registration paths owned
// by a product adapter. Chromium only supplies the registration mechanism.
type StoreConfig struct {
	DefaultStore               string
	UpdateURLs                 map[string]string
	WindowsVendor              string
	MacUserDirectory           string
	MacSystemDirectory         string
	LinuxDirectory             string
	LinuxDirectoryInHome       bool
	LinuxAdditionalDirectories []string
	LinuxLocalCRX              bool
	LinuxUpdateURL             bool
}

func changeStoreInstall(goos, browser string, config StoreConfig, store, id, directory string, remove bool) (extension.InstallResult, error) {
	url, err := storeURL(config, store, id)
	if err != nil {
		return extension.InstallResult{}, err
	}
	var source string
	switch goos {
	case "windows":
		if directory != "" {
			return extension.InstallResult{}, errors.New("--external-dir is for macOS and Linux; Windows uses the machine registry")
		}
		source, err = changeWindowsExternal(config, id, url, remove)
	case "darwin", "linux":
		source, err = changeUnixExternal(goos, browser, config, id, url, directory, remove)
	default:
		return extension.InstallResult{}, fmt.Errorf("external extension requests are unsupported on %s", goos)
	}
	if err != nil {
		return extension.InstallResult{}, err
	}
	if remove {
		return extension.InstallResult{Status: "request-removed", Browser: browser, ID: id, Source: source,
			NextAction: "Restart the browser and check its extensions page. Removing a request does not remove an extension installed independently."}, nil
	}
	status := "awaiting-browser-confirmation"
	next := "Restart the browser, then review and enable the extension in its extensions page."
	if goos == "linux" {
		status = "requested"
		next = "Restart the browser and verify the extension in its extensions page."
	}
	return extension.InstallResult{Status: status, Browser: browser, ID: id, Source: source, NextAction: next}, nil
}

func storeURL(config StoreConfig, store, id string) (string, error) {
	if !chromiumExtensionID.MatchString(id) {
		return "", errors.New("a 32-character extension ID using a-p is required")
	}
	if store == "" {
		store = config.DefaultStore
	}
	url, ok := config.UpdateURLs[store]
	if !ok || url == "" {
		return "", fmt.Errorf("store %q is not supported by this adapter", store)
	}
	return url, nil
}

func defaultExternalDirectory(goos string, config StoreConfig) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	switch goos {
	case "darwin":
		if config.MacUserDirectory != "" {
			return filepath.Join(home, filepath.FromSlash(config.MacUserDirectory)), nil
		}
	case "linux":
		if config.LinuxDirectory != "" {
			if config.LinuxDirectoryInHome {
				return filepath.Join(home, filepath.FromSlash(config.LinuxDirectory)), nil
			}
			return config.LinuxDirectory, nil
		}
	}
	return "", errors.New("no external extensions directory declared for this platform")
}

func allowedExternalDirectory(goos string, config StoreConfig, directory string) bool {
	clean := filepath.Clean(directory)
	defaultPath, err := defaultExternalDirectory(goos, config)
	if err == nil && clean == filepath.Clean(defaultPath) {
		return true
	}
	if goos == "darwin" && config.MacSystemDirectory != "" {
		return clean == filepath.Clean(config.MacSystemDirectory)
	}
	if goos == "linux" {
		for _, path := range config.LinuxAdditionalDirectories {
			if clean == filepath.Clean(path) {
				return true
			}
		}
	}
	return false
}

func checkExternalPath(directory string) error {
	for current := directory; current != filepath.Dir(current); current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		// macOS exposes /var and /tmp as aliases at the filesystem root.
		if info.Mode()&os.ModeSymlink != 0 && filepath.Dir(current) != string(filepath.Separator) {
			return fmt.Errorf("external extensions path contains symlink %q", current)
		}
	}
	return nil
}

func changeUnixExternal(goos, browser string, config StoreConfig, id, url, directory string, remove bool) (string, error) {
	return changeUnixExternalProperties(goos, browser, config, id, map[string]string{"external_update_url": url}, directory, remove)
}

func changeUnixExternalProperties(goos, browser string, config StoreConfig, id string, properties map[string]string, directory string, remove bool) (string, error) {
	if directory == "" {
		var err error
		directory, err = defaultExternalDirectory(goos, config)
		if err != nil {
			return "", err
		}
	}
	if !filepath.IsAbs(directory) || !allowedExternalDirectory(goos, config, directory) {
		return "", fmt.Errorf("external extensions directory is not a documented %s %s path", browser, goos)
	}
	directory = filepath.Clean(directory)
	if err := checkExternalPath(directory); err != nil {
		return "", err
	}
	macSystem := goos == "darwin" && config.MacSystemDirectory != "" && directory == filepath.Clean(config.MacSystemDirectory)
	if macSystem {
		if err := checkMacSystemExternalPath(directory); err != nil {
			return "", err
		}
	}
	filePath := filepath.Join(directory, id+".json")
	if remove {
		if err := checkExternalPreferenceFile(filePath, macSystem); err != nil {
			return "", err
		}
		content, err := os.ReadFile(filePath)
		if err != nil {
			return "", err
		}
		if err := decodeExternalProperties(content, properties); err != nil {
			return "", err
		}
		return filePath, os.Remove(filePath)
	}
	if err := makeExternalDirectories(directory); err != nil {
		return "", err
	}
	if macSystem {
		if err := checkMacSystemExternalPath(directory); err != nil {
			return "", err
		}
	}
	file, err := os.OpenFile(filePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return "", err
	}
	// Chrome's machine preferences must remain readable with a restrictive
	// umask. Only this newly created file is changed.
	if err := file.Chmod(0644); err != nil {
		file.Close()
		os.Remove(filePath)
		return "", err
	}
	content, _ := json.Marshal(properties)
	_, writeErr := file.Write(append(content, '\n'))
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		os.Remove(filePath)
		return "", errors.Join(writeErr, closeErr)
	}
	if err := checkExternalPreferenceFile(filePath, macSystem); err != nil {
		os.Remove(filePath)
		return "", err
	}
	return filePath, nil
}

func changeWindowsExternal(config StoreConfig, id, url string, remove bool) (string, error) {
	script := windowsExternalScript(config, id, url, remove)
	encoded := encodePowerShell(script)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-EncodedCommand", encoded).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("Windows external extension registry update failed (run with administrator rights): %w: %s", err, strings.TrimSpace(string(output)))
	}
	path := strings.TrimSpace(string(output))
	if !strings.HasPrefix(path, `Registry::HKEY_LOCAL_MACHINE\Software\`) || !strings.HasSuffix(path, config.WindowsVendor+`\Extensions\`+id) {
		return "", errors.New("Windows external registration returned an invalid registry location")
	}
	return path, nil
}

func windowsExternalScript(config StoreConfig, id, url string, remove bool) string {
	// Registry32 selects Wow6432Node on a 64-bit OS and the plain Software key
	// on a 32-bit OS, independently of the CTX or PowerShell process bitness.
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "''") + "'" }
	subkey := `Software\` + config.WindowsVendor + `\Extensions\` + id
	script := "$ErrorActionPreference = 'Stop'\n$subkey = " + quote(subkey) + "\n$url = " + quote(url) + `
$hive = [Microsoft.Win32.RegistryKey]::OpenBaseKey([Microsoft.Win32.RegistryHive]::LocalMachine, [Microsoft.Win32.RegistryView]::Registry32)
$key = $null
try {
    $key = $hive.OpenSubKey($subkey, $true)
`
	if remove {
		script += `    if ($null -eq $key) { throw 'External request does not exist' }
    $names = @($key.GetValueNames())
    if ($names.Count -ne 1 -or $names[0] -cne 'update_url' -or $key.SubKeyCount -ne 0) { throw 'External request has additional metadata; refusing to remove it' }
    if ($key.GetValueKind('update_url') -ne [Microsoft.Win32.RegistryValueKind]::String -or $key.GetValue('update_url') -cne $url) { throw 'External request has a different update URL or registry value type' }
    $key.Dispose()
    $key = $null
    $hive.DeleteSubKey($subkey, $true)
`
	} else {
		script += `    if ($null -ne $key) { throw 'External request already exists' }
    $key = $hive.CreateSubKey($subkey)
    try { $key.SetValue('update_url', $url, [Microsoft.Win32.RegistryValueKind]::String) }
    catch {
        if ($key.ValueCount -eq 0 -and $key.SubKeyCount -eq 0) {
            $key.Dispose()
            $key = $null
            $hive.DeleteSubKey($subkey, $false)
        }
        throw
    }
`
	}
	script += `} finally {
    if ($null -ne $key) { $key.Dispose() }
    $hive.Dispose()
}
$base = 'Registry::HKEY_LOCAL_MACHINE\Software\'
if ([Environment]::Is64BitOperatingSystem) { $base += 'Wow6432Node\' }
[Console]::Out.Write($base + ` + quote(config.WindowsVendor+`\Extensions\`+id) + ")\n"
	return script
}

func encodePowerShell(script string) string {
	runes := utf16.Encode([]rune(script))
	bytes := make([]byte, len(runes)*2)
	for i, value := range runes {
		bytes[2*i], bytes[2*i+1] = byte(value), byte(value>>8)
	}
	return base64.StdEncoding.EncodeToString(bytes)
}
