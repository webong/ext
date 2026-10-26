package userscript

import "strings"

const maxMetadataBytes = 64 * 1024

var descriptiveDirectives = map[string]bool{
	"antifeature": true, "author": true, "copyright": true, "defaulticon": true,
	"description": true, "homepage": true, "homepageurl": true, "icon": true,
	"icon64": true, "icon64url": true, "iconurl": true, "license": true,
	"namespace": true, "source": true, "supporturl": true, "tag": true,
	"version": true, "website": true,
}

// CompatibilityReport describes imported userscript metadata without silently
// weakening it. Runnable reports compatibility with res/web's current
// extension-free page-world runtime; inspection never installs the script.
type CompatibilityReport struct {
	Name           string   `json:"name"`
	Grants         []string `json:"grants"`
	Matches        []string `json:"matches"`
	ExcludeMatches []string `json:"excludeMatches"`
	Includes       []string `json:"includes"`
	Excludes       []string `json:"excludes"`
	RunAt          string   `json:"runAt"`
	World          string   `json:"world"`
	AllFrames      bool     `json:"allFrames"`
	Blockers       []string `json:"blockers"`
	Notes          []string `json:"notes"`
}

func (r CompatibilityReport) Runnable() bool { return len(r.Blockers) == 0 }

// InspectCompatibility reads Greasemonkey-family metadata. Unsupported
// requirements are returned as stable blocker codes rather than discarded.
func InspectCompatibility(source string) CompatibilityReport {
	report := CompatibilityReport{
		Grants: []string{}, Matches: []string{}, ExcludeMatches: []string{}, Includes: []string{},
		Excludes: []string{}, AllFrames: true, Blockers: []string{}, Notes: []string{},
	}
	if len(source) == 0 || len(source) > maxSourceBytes {
		report.Blockers = append(report.Blockers, "invalid_source_size")
	}
	inBlock, found, ended, headerBytes := false, false, false, 0
	seenBlocker := map[string]bool{}
	block := func(code string) {
		if !seenBlocker[code] {
			report.Blockers = append(report.Blockers, code)
			seenBlocker[code] = true
		}
	}
	for _, raw := range strings.Split(source, "\n") {
		line := strings.TrimSpace(raw)
		if line == "// ==UserScript==" {
			inBlock, found = true, true
			continue
		}
		if line == "// ==/UserScript==" && inBlock {
			ended = true
			break
		}
		if !inBlock {
			continue
		}
		headerBytes += len(raw) + 1
		if headerBytes > maxMetadataBytes {
			block("metadata_too_large")
			break
		}
		name, value, ok := metadataDirective(line)
		if !ok {
			continue
		}
		directive := strings.ToLower(name)
		switch directive {
		case "name":
			report.Name = value
		case "grant":
			report.Grants = append(report.Grants, value)
			if value != "none" {
				block("unsupported_grant")
			}
		case "match":
			report.Matches = append(report.Matches, value)
		case "exclude-match":
			report.ExcludeMatches = append(report.ExcludeMatches, value)
		case "include":
			report.Includes = append(report.Includes, value)
			block("unsupported_include")
		case "exclude":
			report.Excludes = append(report.Excludes, value)
			block("unsupported_exclude")
		case "noframes":
			report.AllFrames = false
			block("unsupported_noframes")
		case "run-at":
			report.RunAt = value
			if value != "document-start" {
				block("unsupported_run_at")
			}
		case "inject-into":
			report.World = value
			if value != "page" {
				block("unsupported_world")
			}
		case "require":
			block("unsupported_require")
		case "resource":
			block("unsupported_resource")
		case "connect", "run-in", "sandbox", "unwrap", "top-level-await", "webrequest":
			block("unsupported_directive")
		default:
			if !descriptiveDirectives[directive] {
				block("unsupported_directive")
			}
		}
	}
	if !found || !ended {
		block("missing_header")
	}
	return report
}

func metadataDirective(line string) (string, string, bool) {
	if !strings.HasPrefix(line, "// @") {
		return "", "", false
	}
	value := strings.TrimSpace(strings.TrimPrefix(line, "// @"))
	if value == "" {
		return "", "", false
	}
	separator := strings.IndexAny(value, " \t")
	if separator < 0 {
		return value, "", true
	}
	name, rest := value[:separator], value[separator+1:]
	if name == "" {
		return name, "", true
	}
	return name, strings.TrimSpace(rest), true
}
