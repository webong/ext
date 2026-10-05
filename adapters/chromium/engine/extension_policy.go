package chromium

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/webong/ctx/res/browser/extension"
)

// ManageExtensionPolicy changes an administrator-owned Chrome or Edge policy.
// The result records a policy change, not a verified browser installation.
// action is force, unforce, block, or unblock.
func ManageExtensionPolicy(ctx context.Context, config Config, store, id, action string) (extension.InstallResult, error) {
	if err := ctx.Err(); err != nil {
		return extension.InstallResult{}, err
	}
	browser := config.Name
	if config.Extensions.Store == nil || config.Extensions.ManagedPolicyDrivers[runtime.GOOS] == "" {
		return extension.InstallResult{}, errors.New("this adapter has no managed extension policy route on this platform")
	}
	url, err := storeURL(*config.Extensions.Store, store, id)
	if err != nil {
		return extension.InstallResult{}, err
	}
	if action != "force" && action != "unforce" && action != "block" && action != "unblock" {
		return extension.InstallResult{}, errors.New("policy action must be force, unforce, block, or unblock")
	}
	var source string
	switch runtime.GOOS {
	case "windows":
		source, err = manageWindowsPolicy(ctx, config.Extensions.Store.WindowsVendor, id, url, action)
	case "linux":
		if config.Extensions.LinuxPolicyPath == "" {
			return extension.InstallResult{}, errors.New("this adapter has no Linux managed-policy path")
		}
		if os.Geteuid() != 0 {
			return extension.InstallResult{}, errors.New("Linux managed policy requires administrator rights")
		}
		source, err = manageLinuxPolicy(id, url, action, config.Extensions.LinuxPolicyPath)
	case "darwin":
		return extension.InstallResult{}, errors.New("macOS extension policies must be deployed through an administrator-managed configuration profile")
	default:
		return extension.InstallResult{}, errors.New("extension policy adapter is unavailable on this platform")
	}
	if err != nil {
		return extension.InstallResult{}, err
	}
	return extension.InstallResult{Status: "policy-updated", Browser: browser, ID: id, Source: source,
		NextAction: "Check the browser's policy page and extensions page after policy refresh or restart. Policy update is not proof the extension is installed or blocked."}, nil
}

func policyKey(action string) string {
	if action == "force" || action == "unforce" {
		return "ExtensionInstallForcelist"
	}
	return "ExtensionInstallBlocklist"
}

func manageWindowsPolicy(ctx context.Context, vendor, id, url, action string) (string, error) {
	path, script := windowsPolicyScript(vendor, id, url, action)
	output, err := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-EncodedCommand", encodePowerShell(script)).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("Windows extension policy update failed (run with administrator rights): %w: %s", err, strings.TrimSpace(string(output)))
	}
	return path, nil
}

func windowsPolicyScript(vendor, id, url, action string) (string, string) {
	path := `Registry::HKEY_LOCAL_MACHINE\SOFTWARE\Policies\` + vendor + `\` + policyKey(action)
	value := id
	if action == "force" || action == "unforce" {
		value += ";" + url
	}
	script := "$ErrorActionPreference = 'Stop'\n$path = '" + path + "'\n$id = '" + id + "'\n$value = '" + value + "'\n" +
		"if (-not (Test-Path -LiteralPath $path)) {\n" +
		"  if ('" + action + "' -in @('unforce','unblock')) { throw 'Policy entry does not exist' }\n" +
		"  New-Item -Path $path -Force | Out-Null\n}\n" +
		"$item = Get-ItemProperty -LiteralPath $path\n" +
		"$entries = @($item.PSObject.Properties | Where-Object { $_.Name -match '^[0-9]+$' })\n" +
		"$matches = @($entries | Where-Object { ([string]$_.Value).Split(';')[0] -eq $id })\n"
	if action == "force" || action == "block" {
		script += "if ($matches.Count -gt 0) {\n" +
			"  if ($matches.Count -eq 1 -and [string]$matches[0].Value -eq $value) { return }\n" +
			"  throw 'Policy already contains this extension ID with a different value'\n}\n" +
			"$next = 1\nwhile ($entries.Name -contains [string]$next) { $next++ }\n" +
			"New-ItemProperty -LiteralPath $path -Name ([string]$next) -Value $value -PropertyType String -Force | Out-Null\n"
	} else {
		script += "if ($matches.Count -eq 0) { throw 'Policy entry does not exist' }\n" +
			"foreach ($entry in $matches) { Remove-ItemProperty -LiteralPath $path -Name $entry.Name }\n"
	}
	return path, script
}

func manageLinuxPolicy(id, url, action, path string) (string, error) {
	if err := checkExternalPath(filepath.Dir(path)); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return "", err
	}
	key := policyKey(action)
	// Chrome leaves duplicate policy names across files undefined. Never add
	// a competing force or block list beside another administrator's policy.
	files, err := filepath.Glob(filepath.Join(filepath.Dir(path), "*.json"))
	if err != nil {
		return "", err
	}
	for _, file := range files {
		if file == path {
			continue
		}
		content, err := os.ReadFile(file)
		if err != nil {
			return "", err
		}
		var values map[string]json.RawMessage
		if err := json.Unmarshal(content, &values); err != nil {
			return "", fmt.Errorf("cannot inspect policy file %s: %w", file, err)
		}
		if _, exists := values[key]; exists {
			return "", fmt.Errorf("%s is already configured in %s", key, file)
		}
	}
	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
		return "", errors.New("managed policy file must be a regular file")
	} else if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	values := map[string]json.RawMessage{}
	if content, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(content, &values); err != nil {
			return "", err
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if values == nil {
		return "", errors.New("managed policy file must contain a JSON object")
	}
	var list []string
	if data, ok := values[key]; ok {
		if err := json.Unmarshal(data, &list); err != nil {
			return "", err
		}
	}
	index := -1
	for i, item := range list {
		if strings.SplitN(item, ";", 2)[0] == id {
			if index >= 0 {
				return "", errors.New("managed policy contains this extension ID more than once")
			}
			index = i
		}
	}
	remove := action == "unforce" || action == "unblock"
	if remove {
		if index < 0 {
			return "", errors.New("policy entry does not exist")
		}
		list = append(list[:index], list[index+1:]...)
	} else {
		value := id
		if action == "force" {
			value += ";" + url
		}
		if index >= 0 && list[index] != value {
			return "", errors.New("policy already contains this ID with a different value")
		}
		if index < 0 {
			list = append(list, value)
		}
	}
	if len(list) == 0 {
		delete(values, key)
	} else {
		values[key], err = json.Marshal(list)
		if err != nil {
			return "", err
		}
	}
	if len(values) == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return "", err
		}
		return path, nil
	}
	content, err := json.MarshalIndent(values, "", "  ")
	if err != nil {
		return "", err
	}
	// Keep the managed file administrator-owned and world-readable. The caller
	// needs sufficient rights to write the managed directory.
	temp, err := os.CreateTemp(filepath.Dir(path), ".ctx-policy-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(temp.Name())
	if err := temp.Chmod(0644); err != nil {
		temp.Close()
		return "", err
	}
	if _, err := temp.Write(append(content, '\n')); err != nil {
		temp.Close()
		return "", err
	}
	if err := temp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(temp.Name(), path); err != nil {
		return "", err
	}
	return path, nil
}
