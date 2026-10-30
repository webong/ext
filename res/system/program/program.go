// Package program finds executables by name and joins what different sources
// report about the same file.
//
// It is mechanism only. It never branches on a program or installer name: the
// caller says which names to look for, and installers report what they own.
// A Candidate is an observation, not permission to run the file.
package program

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// Source says how a candidate was found.
const (
	SourcePath      = "path"
	SourceInstaller = "installer"
)

// Candidate is one executable file. Path is how the machine reaches it and
// RealPath is the same file with links resolved; two reports describe one
// program exactly when their RealPath values are equal.
type Candidate struct {
	Name       string         `json:"name"`
	Path       string         `json:"path"`
	RealPath   string         `json:"realpath"`
	Version    string         `json:"version,omitempty"`
	Source     string         `json:"source"`
	OnPath     bool           `json:"on_path,omitempty"`
	Installers []string       `json:"installers,omitempty"`
	Attributes map[string]any `json:"attributes,omitempty"`
}

// Scan looks for the named programs in the directories of pathList, a PATH
// style list. It stats files and never runs them. Empty and relative entries
// are skipped, so the result does not depend on the working directory. Every
// distinct file is reported, in PATH order, so the first one is what a shell
// would run; links to one file collapse into the first path that reached it.
func Scan(names []string, pathList string) []Candidate {
	wanted := map[string]bool{}
	for _, name := range names {
		if name != "" && !strings.ContainsAny(name, `/\`) {
			wanted[name] = true
		}
	}
	var found []Candidate
	seen := map[string]bool{}
	for _, dir := range filepath.SplitList(pathList) {
		if dir == "" || !filepath.IsAbs(dir) {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			name := commandName(entry.Name())
			if !wanted[name] {
				continue
			}
			path := filepath.Join(dir, entry.Name())
			candidate, ok := inspect(name, path)
			if !ok || seen[candidate.RealPath+"\x00"+name] {
				continue
			}
			seen[candidate.RealPath+"\x00"+name] = true
			candidate.Source = SourcePath
			candidate.OnPath = true
			found = append(found, candidate)
		}
	}
	return found
}

// Inspect reports a file named by an installer as a candidate. It returns false
// for a path that is not an existing regular executable, so an installer cannot
// claim a file that is not there.
func Inspect(name, path string) (Candidate, bool) {
	if !filepath.IsAbs(path) {
		return Candidate{}, false
	}
	return inspect(name, path)
}

func inspect(name, path string) (Candidate, bool) {
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return Candidate{}, false
	}
	info, err := os.Stat(real)
	if err != nil || !info.Mode().IsRegular() || !executable(real, info) {
		return Candidate{}, false
	}
	return Candidate{Name: name, Path: path, RealPath: real}, true
}

func executable(path string, info os.FileInfo) bool {
	if runtime.GOOS == "windows" {
		return pathExt(filepath.Ext(path))
	}
	return info.Mode().Perm()&0o111 != 0
}

// commandName is the name a user types: the file name without a Windows
// executable extension.
func commandName(file string) string {
	if runtime.GOOS == "windows" && pathExt(filepath.Ext(file)) {
		return strings.TrimSuffix(file, filepath.Ext(file))
	}
	return file
}

func pathExt(ext string) bool {
	list := os.Getenv("PATHEXT")
	if list == "" {
		list = ".COM;.EXE;.BAT;.CMD"
	}
	for _, candidate := range strings.Split(list, ";") {
		if strings.EqualFold(candidate, ext) {
			return true
		}
	}
	return false
}

// Merge joins the PATH scan with what each installer reported. reports maps an
// installer adapter name to its candidates. A candidate an installer reports is
// kept only when the file exists; one found on PATH and by an installer is a
// single result that lists the installer. The result is ordered by name, then
// RealPath, and installers are in name order, so it is stable between scans.
func Merge(scanned []Candidate, reports map[string][]Candidate) []Candidate {
	byKey := map[string]*Candidate{}
	var order []string
	add := func(candidate Candidate) *Candidate {
		key := candidate.Name + "\x00" + candidate.RealPath
		if existing, ok := byKey[key]; ok {
			return existing
		}
		copy := candidate
		copy.Installers = nil
		byKey[key] = &copy
		order = append(order, key)
		return &copy
	}
	for _, candidate := range scanned {
		existing := add(candidate)
		existing.OnPath = true
	}
	installers := make([]string, 0, len(reports))
	for name := range reports {
		installers = append(installers, name)
	}
	sort.Strings(installers)
	for _, installer := range installers {
		for _, reported := range reports[installer] {
			checked, ok := Inspect(reported.Name, reported.Path)
			if !ok {
				continue
			}
			checked.Version = reported.Version
			checked.Attributes = reported.Attributes
			existing := add(checked)
			if existing.Version == "" {
				existing.Version = checked.Version
			}
			if existing.Attributes == nil {
				existing.Attributes = checked.Attributes
			}
			if !contains(existing.Installers, installer) {
				existing.Installers = append(existing.Installers, installer)
			}
		}
	}
	merged := make([]Candidate, 0, len(order))
	for _, key := range order {
		candidate := *byKey[key]
		candidate.Source = SourcePath
		if len(candidate.Installers) > 0 {
			candidate.Source = SourceInstaller
		}
		merged = append(merged, candidate)
	}
	sort.Slice(merged, func(i, j int) bool {
		if merged[i].Name != merged[j].Name {
			return merged[i].Name < merged[j].Name
		}
		return merged[i].RealPath < merged[j].RealPath
	})
	return merged
}

// ForCommands keeps the candidates whose name is one of commands.
func ForCommands(candidates []Candidate, commands []string) []Candidate {
	var kept []Candidate
	for _, candidate := range candidates {
		if contains(commands, candidate.Name) {
			kept = append(kept, candidate)
		}
	}
	return kept
}

func contains(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}
