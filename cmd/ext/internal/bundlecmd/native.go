package bundlecmd

import (
	"encoding/json"
	"fmt"
	"go/token"
	"regexp"
	"strings"

	"github.com/webong/ext/pkg/ext-go/cengine/fetch"
	"github.com/webong/ext/pkg/ext-go/cengine/library"
)

type NativeJob struct {
	Citation json.RawMessage `json:"citation"`
	Drivers  []string        `json:"drivers"`
	Dir      string          `json:"dir"`
	Package  string          `json:"package,omitempty"`
}

type NativePlan struct {
	Library    string            `json:"library"`
	Version    string            `json:"version"`
	Dir        string            `json:"dir"`
	Package    string            `json:"package"`
	Selections []fetch.Selection `json:"selections"`
}

var nativePackageName = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// planNative performs no IO. The C bundle validator owns the job envelope;
// native citations and target selections reuse the library fetcher's contract.
func planNative(bundle Bundle) ([]NativePlan, error) {
	plans := make([]NativePlan, 0, len(bundle.Native))
	dirs := []string{}
	libraries := map[string]bool{}
	for _, job := range bundle.Native {
		if err := nativeRelative(job.Dir); err != nil {
			return nil, err
		}
		citation, err := library.ParseCitation(job.Citation)
		if err != nil {
			return nil, err
		}
		if libraries[citation.Library] {
			return nil, fmt.Errorf("duplicate native library %s", citation.Library)
		}
		libraries[citation.Library] = true
		dir := strings.ToLower(job.Dir)
		for _, other := range dirs {
			if dir == other || strings.HasPrefix(dir, other+"/") || strings.HasPrefix(other, dir+"/") {
				return nil, fmt.Errorf("overlapping native destinations: %s and %s", job.Dir, other)
			}
		}
		dirs = append(dirs, dir)
		name := job.Package
		if name == "" {
			name = strings.TrimPrefix(citation.Library, "ext_")
		}
		if !nativePackageName.MatchString(name) || token.Lookup(name).IsKeyword() {
			return nil, fmt.Errorf("invalid generated Go package name %q", name)
		}
		selections, err := fetch.Plan(citation, job.Drivers, []string{bundle.Target})
		if err != nil {
			return nil, err
		}
		plans = append(plans, NativePlan{Library: citation.Library, Version: citation.Version, Dir: job.Dir, Package: name, Selections: selections})
	}
	return plans, nil
}
