package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/webong/ext/res/system/service/program"
)

// A configured installer is one the user described in their own home instead of
// packaging it as an adapter: a name and the directory patterns where it keeps
// programs. It is the same declaration a declarative installer adapter makes, read
// from a file only the user writes, so it needs no trust step.

const (
	maxConfiguredInstallers = 256
	maxInstallerRoots       = 64
	maxInstallerRootBytes   = 512
)

type configuredInstaller struct {
	Name  string   `json:"name"`
	Roots []string `json:"roots"`
}

type installerFile struct {
	Version    int                   `json:"version"`
	Installers []configuredInstaller `json:"installers"`
}

func installerConfigPath() string {
	return filepath.Join(configHomePath(), "installers.json")
}

func validateConfiguredInstaller(item configuredInstaller) error {
	if !managerInstanceName.MatchString(item.Name) {
		return fmt.Errorf("%q is not an installer name", item.Name)
	}
	if len(item.Roots) == 0 || len(item.Roots) > maxInstallerRoots {
		return fmt.Errorf("installer %s needs 1 to %d directory patterns", item.Name, maxInstallerRoots)
	}
	for _, root := range item.Roots {
		if root == "" || len(root) > maxInstallerRootBytes || strings.ContainsAny(root, "\x00\r\n") {
			return fmt.Errorf("installer %s has an invalid pattern %q", item.Name, root)
		}
		// A pattern is absolute once ~ and variables are expanded; one that starts
		// with anything else would depend on the working directory.
		if !(filepath.IsAbs(root) || strings.HasPrefix(root, "~") || strings.HasPrefix(root, "$")) {
			return fmt.Errorf("installer %s pattern %q must start with /, ~ or a variable", item.Name, root)
		}
	}
	return nil
}

func loadInstallerFile() (installerFile, error) {
	empty := installerFile{Version: 1}
	path := installerConfigPath()
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return empty, nil
	}
	if err != nil {
		return empty, err
	}
	if !info.Mode().IsRegular() {
		return empty, fmt.Errorf("installer configuration is not a regular file: %s", path)
	}
	file, err := os.Open(path)
	if err != nil {
		return empty, err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, 1<<20))
	decoder.DisallowUnknownFields()
	var loaded installerFile
	if err := decoder.Decode(&loaded); err != nil {
		return empty, fmt.Errorf("read installer configuration: %w", err)
	}
	if loaded.Version != 1 || len(loaded.Installers) > maxConfiguredInstallers {
		return empty, fmt.Errorf("unsupported installer configuration in %s", path)
	}
	seen := map[string]bool{}
	for _, item := range loaded.Installers {
		if err := validateConfiguredInstaller(item); err != nil {
			return empty, err
		}
		if seen[item.Name] {
			return empty, fmt.Errorf("duplicate installer %s", item.Name)
		}
		seen[item.Name] = true
	}
	return loaded, nil
}

// readConfiguredInstallers is the installers as discovery uses them. A file that
// cannot be read declares none: discovery never fails because of it.
func readConfiguredInstallers() []configuredInstaller {
	loaded, err := loadInstallerFile()
	if err != nil {
		return nil
	}
	return loaded.Installers
}

func saveInstallerFile(file installerFile) error {
	path := installerConfigPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	sort.Slice(file.Installers, func(i, j int) bool { return file.Installers[i].Name < file.Installers[j].Name })
	if file.Installers == nil {
		file.Installers = []configuredInstaller{}
	}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".ctx-installers-")
	if err != nil {
		return err
	}
	defer os.Remove(temporary.Name())
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(append(data, '\n')); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return replaceManagerRegistry(temporary.Name(), path)
}

// configuredInstallerReports scans each installer's directories for the names and
// returns what each found, the way an installer adapter's report is joined with
// the PATH scan.
func configuredInstallerReports(installers []configuredInstaller, names []string, env func(string) string) map[string][]program.Candidate {
	reports := map[string][]program.Candidate{}
	for _, item := range installers {
		reports[item.Name] = program.ScanRoots(names, item.Roots, env)
	}
	return reports
}
