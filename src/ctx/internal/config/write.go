package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

func SetFlat(path, key, value string) error {
	return SetFlatValues(path, map[string]string{key: value})
}

func SetFlatValues(path string, values map[string]string) error {
	return mutate(path, func(lines []string) ([]string, bool, error) {
		keys := make([]string, 0, len(values))
		for key := range values {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			lines = setValue(lines, 0, len(lines), key, values[key])
		}
		return lines, false, nil
	})
}

func RemoveFlat(path string, keys ...string) error {
	wanted := make(map[string]bool, len(keys))
	for _, key := range keys {
		wanted[key] = true
	}
	return mutate(path, func(lines []string) ([]string, bool, error) {
		result := make([]string, 0, len(lines))
		for _, line := range lines {
			key, _, ok := parseAssignment(strings.TrimSpace(line))
			if ok && wanted[key] {
				continue
			}
			result = append(result, line)
		}
		remove := true
		for _, line := range result {
			if _, _, ok := parseAssignment(strings.TrimSpace(line)); ok {
				remove = false
				break
			}
		}
		return result, remove, nil
	})
}

func SetRoot(path, key, value string) error {
	return mutate(path, func(lines []string) ([]string, bool, error) {
		end := firstSection(lines)
		updated := setValue(lines, 0, end, key, value)
		return updated, false, nil
	})
}

func SetSection(path, header, key, value string) error {
	return mutate(path, func(lines []string) ([]string, bool, error) {
		start, end, found := sectionRange(lines, header)
		if !found {
			if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) != "" {
				lines = append(lines, "")
			}
			lines = append(lines, header, formatAssignment(key, value))
			return lines, false, nil
		}
		return setValue(lines, start+1, end, key, value), false, nil
	})
}

func RemoveSectionValue(path, header, key string) error {
	return mutate(path, func(lines []string) ([]string, bool, error) {
		start, end, found := sectionRange(lines, header)
		if !found {
			return lines, false, nil
		}
		result := make([]string, 0, len(lines))
		result = append(result, lines[:start+1]...)
		for _, line := range lines[start+1 : end] {
			lineKey, _, ok := parseAssignment(strings.TrimSpace(line))
			if ok && lineKey == key {
				continue
			}
			result = append(result, line)
		}
		result = append(result, lines[end:]...)
		return result, false, nil
	})
}

func SectionLines(path, header string) ([]string, error) {
	lines, err := readLines(path)
	if err != nil {
		return nil, err
	}
	start, end, found := sectionRange(lines, header)
	if !found {
		return nil, nil
	}
	result := make([]string, 0, end-start-1)
	for _, line := range lines[start+1 : end] {
		if strings.TrimSpace(line) != "" {
			result = append(result, line)
		}
	}
	return result, nil
}

func setValue(lines []string, start, end int, key, value string) []string {
	replacement := formatAssignment(key, value)
	result := make([]string, 0, len(lines)+1)
	result = append(result, lines[:start]...)
	written := false
	for _, line := range lines[start:end] {
		lineKey, _, ok := parseAssignment(strings.TrimSpace(line))
		if ok && lineKey == key {
			if !written {
				result = append(result, replacement)
				written = true
			}
			continue
		}
		result = append(result, line)
	}
	if !written {
		result = append(result, replacement)
	}
	result = append(result, lines[end:]...)
	return result
}

func formatAssignment(key, value string) string { return key + " = " + strconv.Quote(value) }

func firstSection(lines []string) int {
	for index, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "[") {
			return index
		}
	}
	return len(lines)
}

func sectionRange(lines []string, header string) (int, int, bool) {
	for index, line := range lines {
		if strings.TrimSpace(line) != header {
			continue
		}
		end := len(lines)
		for next := index + 1; next < len(lines); next++ {
			if strings.HasPrefix(strings.TrimSpace(lines[next]), "[") {
				end = next
				break
			}
		}
		return index, end, true
	}
	return 0, 0, false
}

func mutate(path string, change func([]string) ([]string, bool, error)) error {
	if err := validatePath(path); err != nil {
		return err
	}
	return withLock(path, func() error {
		lines, err := readLines(path)
		if err != nil {
			return err
		}
		updated, remove, err := change(lines)
		if err != nil {
			return err
		}
		if remove {
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			return nil
		}
		return atomicWrite(path, updated)
	})
}

func readLines(path string) ([]string, error) {
	contents, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	text := strings.ReplaceAll(string(contents), "\r\n", "\n")
	text = strings.TrimSuffix(text, "\n")
	if text == "" {
		return nil, nil
	}
	return strings.Split(text, "\n"), nil
}

func atomicWrite(path string, lines []string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".ctx-write-")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.WriteString(strings.Join(lines, "\n") + "\n"); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return replaceConfigFile(temporaryPath, path)
}

func withLock(path string, action func() error) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	lockPath := path + ".lock"
	deadline := time.Now().Add(2 * time.Second)
	for {
		lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			fmt.Fprintf(lock, "%d\n", os.Getpid())
			lock.Close()
			defer os.Remove(lockPath)
			return action()
		}
		if !errors.Is(err, os.ErrExist) {
			return err
		}
		if info, statErr := os.Stat(lockPath); statErr == nil && time.Since(info.ModTime()) > 30*time.Second {
			_ = os.Remove(lockPath)
			continue
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("configuration is locked by another ctx process: %s", lockPath)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func validatePath(path string) error {
	if strings.ContainsAny(path, "\x00") {
		return errors.New("invalid configuration path")
	}
	return nil
}
