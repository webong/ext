// Package program checks declared software locations, finds executables by
// name and joins what different sources report about the same file.
//
// It is mechanism only. It never branches on a program or installer name: the
// caller says which names to look for, and installers report what they own.
// A Candidate is an observation, not permission to run the file.
package program

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/webong/ext/pkg/ext-go/cengine/ledger"
	"github.com/webong/ext/src/ctx/internal/graphapi"
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
func Scan(names []string, pathList string) ([]Candidate, error) {
	lib, err := graphapi.Library()
	if err != nil {
		return nil, err
	}
	observed, err := lib.ScanPrograms(context.Background(), names, pathList)
	if err != nil {
		return nil, err
	}
	result := make([]Candidate, 0, len(observed))
	for _, item := range observed {
		result = append(result, Candidate{Name: item.Name, Path: item.Path, RealPath: item.RealPath, Source: SourcePath, OnPath: true})
	}
	return result, nil
}

const maxRootDirectories = 1024

// ExpandRoot turns a directory pattern from a declarative installer into an
// absolute glob pattern. It understands a leading ~, $NAME, ${NAME} and
// ${NAME:-default}, where the default may itself start with ~. A value taken from
// the environment is matched literally, never as a glob. It returns false when a
// variable without a default is unset or the result is not absolute, so a
// pattern never depends on the working directory.
func ExpandRoot(pattern string, env func(string) string) (string, bool) {
	var out strings.Builder
	literal := func(value string) {
		if runtime.GOOS == "windows" {
			out.WriteString(value)
			return
		}
		for _, r := range value {
			if strings.ContainsRune(`*?[\`, r) {
				out.WriteByte('\\')
			}
			out.WriteRune(r)
		}
	}
	home := func() (string, bool) {
		value := env("HOME")
		if value == "" {
			value = env("USERPROFILE")
		}
		return value, value != ""
	}
	if pattern == "~" || strings.HasPrefix(pattern, "~/") || strings.HasPrefix(pattern, `~\`) {
		dir, ok := home()
		if !ok {
			return "", false
		}
		literal(dir)
		pattern = pattern[1:]
	}
	for len(pattern) > 0 {
		index := strings.IndexByte(pattern, '$')
		if index < 0 {
			out.WriteString(pattern)
			break
		}
		out.WriteString(pattern[:index])
		pattern = pattern[index+1:]
		var name, fallback string
		hasFallback := false
		if strings.HasPrefix(pattern, "{") {
			end := strings.IndexByte(pattern, '}')
			if end < 0 {
				return "", false
			}
			name = pattern[1:end]
			pattern = pattern[end+1:]
			if cut := strings.Index(name, ":-"); cut >= 0 {
				name, fallback, hasFallback = name[:cut], name[cut+2:], true
			}
		} else {
			end := 0
			for end < len(pattern) && (pattern[end] == '_' || pattern[end] >= 'A' && pattern[end] <= 'Z' || pattern[end] >= 'a' && pattern[end] <= 'z' || end > 0 && pattern[end] >= '0' && pattern[end] <= '9') {
				end++
			}
			name, pattern = pattern[:end], pattern[end:]
		}
		if name == "" {
			return "", false
		}
		value := env(name)
		if value == "" && hasFallback {
			if strings.HasPrefix(fallback, "~") {
				dir, ok := home()
				if !ok {
					return "", false
				}
				fallback = dir + fallback[1:]
			}
			value = fallback
		}
		if value == "" {
			return "", false
		}
		literal(value)
	}
	expanded := out.String()
	if !filepath.IsAbs(expanded) {
		return "", false
	}
	return expanded, true
}

// ScanRoots looks for the named programs in the directories that patterns match.
// A pattern is expanded with ExpandRoot and matched with filepath.Glob; only
// directories count, at most maxRootDirectories of them. It stats files and never
// runs them. Candidates carry no version: the installer's directories do not say
// more than the layout allows, and the runtime adapter that asked probes the file.
func ScanRoots(names, patterns []string, env func(string) string) ([]Candidate, error) {
	lib, err := graphapi.Library()
	if err != nil {
		return nil, err
	}
	var found []Candidate
	seen := map[string]bool{}
	directories := 0
	for _, pattern := range patterns {
		expanded, ok := ExpandRoot(pattern, env)
		if !ok {
			continue
		}
		matches, err := filepath.Glob(expanded)
		if err != nil {
			continue
		}
		sort.Strings(matches)
		for _, dir := range matches {
			if info, err := os.Stat(dir); err != nil || !info.IsDir() {
				continue
			}
			if directories++; directories > maxRootDirectories {
				return found, nil
			}
			programs, err := lib.ScanPrograms(context.Background(), names, dir)
			if err != nil {
				return nil, err
			}
			for _, item := range programs {
				key := item.RealPath + "\x00" + item.Name
				if seen[key] {
					continue
				}
				seen[key] = true
				found = append(found, Candidate{Name: item.Name, Path: item.Path, RealPath: item.RealPath, Source: SourceInstaller})
			}
		}
	}
	return found, nil
}

// Inspect reports a file named by an installer as a candidate. It returns false
// for a path that is not an existing regular executable, so an installer cannot
// claim a file that is not there.
func Inspect(name, path string) (Candidate, bool, error) {
	lib, err := graphapi.Library()
	if err != nil {
		return Candidate{}, false, err
	}
	item, err := lib.InspectProgram(context.Background(), name, path)
	if err != nil {
		if ledger.Code(err) == "not_found" || ledger.Code(err) == "invalid" {
			return Candidate{}, false, nil
		}
		return Candidate{}, false, err
	}
	return Candidate{Name: item.Name, Path: item.Path, RealPath: item.RealPath}, true, nil
}

// Merge joins the PATH scan with what each installer reported. reports maps an
// installer adapter name to its candidates. A candidate an installer reports is
// kept only when the file exists; one found on PATH and by an installer is a
// single result that lists the installer. The result is ordered by name, then
// RealPath, and installers are in name order, so it is stable between scans.
func Merge(scanned []Candidate, reports map[string][]Candidate) ([]Candidate, error) {
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
			checked, ok, err := Inspect(reported.Name, reported.Path)
			if err != nil {
				return nil, err
			}
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
	return merged, nil
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
