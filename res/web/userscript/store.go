// Package userscript validates and stores caller-approved scripts for
// session-scoped replay through a browser adapter.
package userscript

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const maxSourceBytes = 1024 * 1024

var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
var matchPattern = regexp.MustCompile(`^(\*|https?|file)://([^/]+|\*)/(.*)$`)

type Record struct {
	Target         string   `json:"target"`
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Revision       string   `json:"revision"`
	Matches        []string `json:"matches"`
	ExcludeMatches []string `json:"excludeMatches"`
	Enabled        bool     `json:"enabled"`
	Source         string   `json:"source"`
}

type Description struct {
	Status         string   `json:"status,omitempty"`
	Target         string   `json:"target"`
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Revision       string   `json:"revision"`
	Matches        []string `json:"matches"`
	ExcludeMatches []string `json:"excludeMatches"`
	Enabled        bool     `json:"enabled"`
	Bytes          int      `json:"bytes"`
	Lifetime       string   `json:"lifetime"`
}

func (record Record) Description() Description {
	return Description{Target: record.Target, ID: record.ID, Name: record.Name,
		Revision: record.Revision, Matches: record.Matches, ExcludeMatches: record.ExcludeMatches,
		Enabled: record.Enabled, Bytes: len(record.Source), Lifetime: "connected-session"}
}

func DefaultDirectory() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "ctx", "userscripts"), nil
}

// Revision binds the review to the exact source, target, identity, and URL scope.
func Revision(record Record) string {
	payload, _ := json.Marshal(struct {
		Target         string   `json:"target"`
		ID             string   `json:"id"`
		Name           string   `json:"name"`
		Matches        []string `json:"matches"`
		ExcludeMatches []string `json:"excludeMatches"`
		Source         string   `json:"source"`
	}{record.Target, record.ID, record.Name, record.Matches, record.ExcludeMatches, record.Source})
	digest := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func Validate(record Record) error {
	if strings.TrimSpace(record.Target) == "" || record.Target != strings.TrimSpace(record.Target) || len(record.Target) > 512 {
		return errors.New("userscript target must be a non-empty stable identifier")
	}
	if !idPattern.MatchString(record.ID) {
		return errors.New("userscript id must contain 1–128 letters, digits, dots, underscores, or hyphens")
	}
	if strings.TrimSpace(record.Name) == "" || len(record.Name) > 128 {
		return errors.New("userscript name must contain 1–128 characters")
	}
	if len(record.Source) == 0 || len(record.Source) > maxSourceBytes {
		return errors.New("userscript source must contain 1 byte to 1 MiB")
	}
	if record.Revision != Revision(record) {
		return errors.New("userscript revision does not match source, target, or permissions")
	}
	if len(record.Matches) == 0 || len(record.Matches) > 128 || len(record.ExcludeMatches) > 128 {
		return errors.New("userscript requires 1–128 matches and at most 128 exclusions")
	}
	seen := map[string]bool{}
	for _, pattern := range append(append([]string{}, record.Matches...), record.ExcludeMatches...) {
		if len(pattern) > 512 || !matchPattern.MatchString(pattern) || seen[pattern] {
			return fmt.Errorf("invalid or repeated userscript match pattern %q", pattern)
		}
		if strings.HasPrefix(pattern, "file://") && !strings.HasPrefix(pattern, "file://*/") {
			return errors.New("file userscript match host must be *")
		}
		seen[pattern] = true
	}
	for _, line := range strings.Split(record.Source, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "// @grant ") && trimmed != "// @grant none" {
			return errors.New("extension-free userscripts support @grant none only")
		}
		if strings.HasPrefix(trimmed, "// @require ") || strings.HasPrefix(trimmed, "// @resource ") {
			return errors.New("extension-free userscripts do not support @require or @resource")
		}
		if strings.HasPrefix(trimmed, "// @include ") || strings.HasPrefix(trimmed, "// @exclude ") ||
			strings.HasPrefix(trimmed, "// @connect ") || strings.HasPrefix(trimmed, "// @updateURL ") ||
			strings.HasPrefix(trimmed, "// @downloadURL ") || trimmed == "// @noframes" {
			return errors.New("extension-free userscript metadata contains an unsupported directive")
		}
		if strings.HasPrefix(trimmed, "// @run-at ") && trimmed != "// @run-at document-start" {
			return errors.New("extension-free userscripts run at document-start only")
		}
		if strings.HasPrefix(trimmed, "// @inject-into ") && trimmed != "// @inject-into page" {
			return errors.New("extension-free userscripts run in the page world only")
		}
	}
	return validateSourceMetadata(record)
}

func validateSourceMetadata(record Record) error {
	lines := strings.Split(record.Source, "\n")
	inBlock, found := false, false
	var matches, excludes []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "// ==UserScript==" {
			inBlock, found = true, true
			continue
		}
		if line == "// ==/UserScript==" {
			inBlock = false
			break
		}
		if !inBlock || !strings.HasPrefix(line, "// @") {
			continue
		}
		if value, ok := strings.CutPrefix(line, "// @match "); ok {
			matches = append(matches, strings.TrimSpace(value))
		}
		if value, ok := strings.CutPrefix(line, "// @exclude-match "); ok {
			excludes = append(excludes, strings.TrimSpace(value))
		}
	}
	if !found {
		return nil
	}
	if !sameStrings(matches, record.Matches) || !sameStrings(excludes, record.ExcludeMatches) {
		return errors.New("userscript source match metadata must match --match and --exclude-match")
	}
	return nil
}

func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	a, b := append([]string{}, left...), append([]string{}, right...)
	sort.Strings(a)
	sort.Strings(b)
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func filePath(directory, target, id string) (string, error) {
	if strings.TrimSpace(target) == "" || !idPattern.MatchString(id) {
		return "", errors.New("userscript target and valid id are required")
	}
	digest := sha256.Sum256([]byte(target))
	return filepath.Join(directory, hex.EncodeToString(digest[:]), id+".json"), nil
}

func Load(directory, target, id string) (Record, error) {
	path, err := filePath(directory, target, id)
	if err != nil {
		return Record{}, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return Record{}, err
	}
	if !info.Mode().IsRegular() {
		return Record{}, errors.New("userscript record is not a regular file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Record{}, err
	}
	var record Record
	if err := json.Unmarshal(data, &record); err != nil {
		return Record{}, err
	}
	if record.Target != target || record.ID != id {
		return Record{}, errors.New("userscript record target or id mismatch")
	}
	if err := Validate(record); err != nil {
		return Record{}, err
	}
	return record, nil
}

func List(directory, target string) ([]Record, error) {
	if strings.TrimSpace(target) == "" {
		return nil, errors.New("userscript target is required")
	}
	digest := sha256.Sum256([]byte(target))
	entries, err := os.ReadDir(filepath.Join(directory, hex.EncodeToString(digest[:])))
	if errors.Is(err, os.ErrNotExist) {
		return []Record{}, nil
	}
	if err != nil {
		return nil, err
	}
	result := make([]Record, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		record, err := Load(directory, target, id)
		if err != nil {
			return nil, fmt.Errorf("load userscript %q: %w", id, err)
		}
		result = append(result, record)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}

func Save(directory string, record Record, replace bool) error {
	if err := Validate(record); err != nil {
		return err
	}
	path, err := filePath(directory, record.Target, record.ID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() {
			return errors.New("userscript record is not a regular file")
		}
		if !replace {
			return fmt.Errorf("userscript %q is already installed", record.ID)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	} else if replace {
		return fmt.Errorf("userscript %q is not installed", record.ID)
	}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".userscript-*")
	if err != nil {
		return err
	}
	defer os.Remove(temporary.Name())
	defer temporary.Close()
	if err := temporary.Chmod(0600); err != nil {
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporary.Name(), path)
}

func SetEnabled(directory, target, id string, enabled bool) (Record, error) {
	record, err := Load(directory, target, id)
	if err != nil {
		return Record{}, err
	}
	record.Enabled = enabled
	return record, Save(directory, record, true)
}

func Remove(directory, target, id string) error {
	path, err := filePath(directory, target, id)
	if err != nil {
		return err
	}
	if _, err := Load(directory, target, id); err != nil {
		return err
	}
	return os.Remove(path)
}
