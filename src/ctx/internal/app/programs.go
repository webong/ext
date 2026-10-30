package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode"

	"github.com/webong/ext/ctx/internal/config"
	modpkg "github.com/webong/ext/pkg/ext-go/engine/host/adapter"
	"github.com/webong/ext/res/system/program"
)

// programsEnvironment is the variable a runtime adapter reads to learn which
// copies of its programs exist on the machine, as lines described at
// programsEnvironmentValue. Unset, an adapter looks at PATH only.
const programsEnvironment = "CTX_PROGRAMS"

var programCache struct {
	sync.Mutex
	byNames map[string][]program.Candidate
}

// adapterPrograms returns the candidates for the commands an adapter declares,
// or nil when it declares none. Installers are never given candidates: they are
// the source of them.
func adapterPrograms(resolver *config.Resolver, candidate *modpkg.Adapter) []program.Candidate {
	if len(candidate.Manifest.ComputerCommands) == 0 || candidate.HasRole("installer") {
		return nil
	}
	return discoverPrograms(resolver, candidate.Manifest.ComputerCommands)
}

// discoverPrograms joins a scan of PATH with what each trusted installer adapter
// reports for the names. Candidates are observations; CTX never runs them. The
// result is kept for the life of the process because a scan fans out to every
// runtime adapter.
func discoverPrograms(resolver *config.Resolver, names []string) []program.Candidate {
	names = uniqueSorted(names)
	key := strings.Join(names, "\x00")
	programCache.Lock()
	defer programCache.Unlock()
	if cached, ok := programCache.byNames[key]; ok {
		return cached
	}
	scanned := program.Scan(names, pathWithoutShims())
	reports := map[string][]program.Candidate{}
	store := adapterStore()
	if installed, err := store.List(); err == nil {
		for _, installer := range installed {
			if !installer.HasRole("installer") || !installer.HasCapability("programs") {
				continue
			}
			if trusted, err := store.IsTrusted(installer); err != nil || !trusted {
				continue
			}
			if reported, ok := installerPrograms(resolver, installer, names); ok {
				reports[installer.Manifest.Name] = reported
			}
		}
	}
	merged := program.Merge(scanned, reports)
	if programCache.byNames == nil {
		programCache.byNames = map[string][]program.Candidate{}
	}
	programCache.byNames[key] = merged
	return merged
}

type installerProgramsDocument struct {
	Version  int `json:"version"`
	Programs []struct {
		Name       string         `json:"name"`
		Path       string         `json:"path"`
		Version    string         `json:"version,omitempty"`
		Attributes map[string]any `json:"attributes,omitempty"`
	} `json:"programs"`
}

// installerPrograms runs the installer's programs operation and keeps only
// records for names that were asked about. A response that does not parse is
// ignored whole, as for observe.
func installerPrograms(resolver *config.Resolver, installer *modpkg.Adapter, names []string) ([]program.Candidate, bool) {
	var output, failure boundedObservationBuffer
	if code := invokeAdapter(resolver, installer, "programs", "", names, "", &output, &failure); code != 0 {
		return nil, false
	}
	if len(output.Bytes()) > maxAdapterObservationBytes {
		return nil, false
	}
	decoder := json.NewDecoder(bytes.NewReader(output.Bytes()))
	decoder.DisallowUnknownFields()
	var document installerProgramsDocument
	if err := decoder.Decode(&document); err != nil || document.Version != 1 || len(document.Programs) > 4096 {
		return nil, false
	}
	asked := map[string]bool{}
	for _, name := range names {
		asked[name] = true
	}
	var reported []program.Candidate
	for _, item := range document.Programs {
		if !asked[item.Name] || !validObservationName(item.Path) {
			continue
		}
		reported = append(reported, program.Candidate{Name: item.Name, Path: item.Path, Version: item.Version, Attributes: item.Attributes})
	}
	return reported, true
}

// pathWithoutShims is PATH without the directory CTX itself lives in, where
// command shims point back at CTX and are not programs.
func pathWithoutShims() string {
	self, _ := os.Executable()
	selfDir := filepath.Dir(self)
	var kept []string
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if absolute, err := filepath.Abs(dir); dir != "" && err == nil && strings.EqualFold(filepath.Clean(absolute), filepath.Clean(selfDir)) {
			continue
		}
		kept = append(kept, dir)
	}
	return strings.Join(kept, string(os.PathListSeparator))
}

func uniqueSorted(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, value := range values {
		if value != "" && !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	sort.Strings(out)
	return out
}

// programsEnvironmentValue is the value of CTX_PROGRAMS: one line per candidate
// with tab-separated name, path, realpath, version, installers (comma-joined)
// and on_path (1 or 0), so a shell adapter can read it with no JSON tool. A
// candidate with a control character in any field is left out. The value is
// empty when there are no candidates; the variable is set whenever CTX looked.
func programsEnvironmentValue(candidates []program.Candidate) string {
	var lines []string
	for _, candidate := range candidates {
		onPath := "0"
		if candidate.OnPath {
			onPath = "1"
		}
		fields := []string{candidate.Name, candidate.Path, candidate.RealPath, candidate.Version, strings.Join(candidate.Installers, ","), onPath}
		clean := true
		for _, field := range fields {
			if strings.ContainsFunc(field, unicode.IsControl) {
				clean = false
			}
		}
		if clean {
			lines = append(lines, strings.Join(fields, "\t"))
		}
	}
	return strings.Join(lines, "\n")
}
