package userscript

import (
	"path/filepath"
	"regexp"
	"strings"
)

var metadataIDPattern = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// InferMetadata fills omitted identity and scope from a userscript header.
// Explicit fields are retained and Validate checks them against source metadata.
func InferMetadata(record Record, sourceFile string) Record {
	var name string
	var matches, excludes []string
	inBlock := false
	for _, line := range strings.Split(record.Source, "\n") {
		line = strings.TrimSpace(line)
		if line == "// ==UserScript==" {
			inBlock = true
			continue
		}
		if line == "// ==/UserScript==" {
			break
		}
		if !inBlock {
			continue
		}
		if value, ok := strings.CutPrefix(line, "// @name "); ok {
			name = strings.TrimSpace(value)
		}
		if value, ok := strings.CutPrefix(line, "// @match "); ok {
			matches = append(matches, strings.TrimSpace(value))
		}
		if value, ok := strings.CutPrefix(line, "// @exclude-match "); ok {
			excludes = append(excludes, strings.TrimSpace(value))
		}
	}
	if record.Name == "" {
		record.Name = name
		if record.Name == "" && sourceFile != "" {
			record.Name = strings.TrimSuffix(filepath.Base(sourceFile), filepath.Ext(sourceFile))
		}
	}
	if record.ID == "" {
		record.ID = strings.Trim(metadataIDPattern.ReplaceAllString(strings.ToLower(record.Name), "-"), "-.")
	}
	if record.Matches == nil {
		record.Matches = matches
	}
	if record.ExcludeMatches == nil {
		record.ExcludeMatches = excludes
	}
	return record
}
