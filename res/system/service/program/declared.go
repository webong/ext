package program

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	maxDeclaredLocations  = 128
	maxDeclaredSearchDirs = 128
	maxDeclaredBytes      = 64 << 10
)

// ErrDiscoveryLimit means the declaration or search path exceeded its bounds.
var ErrDiscoveryLimit = errors.New("program discovery limit exceeded")

// MatchDeclared returns the first available location in declaration order.
// An absolute path matches if it exists, including a directory. A bare
// command name matches the first executable in pathList, in PATH order.
// Declarations are trimmed. Empty entries, exact duplicates, relative
// literal paths, and relative search directories are skipped.
//
// It checks at most 128 declarations and 128 search path entries. Each list
// is limited to 64 KiB of input. It checks ctx between filesystem lookups.
// An unsuccessful lookup is a miss. If nothing matches it returns "", nil.
// A match is presence evidence only.
func MatchDeclared(ctx context.Context, locations []string, pathList string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if len(locations) > maxDeclaredLocations || len(pathList) > maxDeclaredBytes {
		return "", ErrDiscoveryLimit
	}
	remaining := maxDeclaredBytes
	for _, location := range locations {
		remaining -= len(location)
		if remaining < 0 {
			return "", ErrDiscoveryLimit
		}
	}

	dirs := filepath.SplitList(pathList)
	if len(dirs) > maxDeclaredSearchDirs {
		return "", ErrDiscoveryLimit
	}
	seenDirs := make(map[string]bool, len(dirs))
	search := dirs[:0]
	for _, dir := range dirs {
		if !filepath.IsAbs(dir) || seenDirs[dir] {
			continue
		}
		seenDirs[dir] = true
		search = append(search, dir)
	}

	seen := make(map[string]bool, len(locations))
	for _, location := range locations {
		location = strings.TrimSpace(location)
		if location == "" || seen[location] {
			continue
		}
		seen[location] = true
		match := matchLocation(ctx, location, search)
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if match != "" {
			return match, nil
		}
	}
	return "", ctx.Err()
}

func matchLocation(ctx context.Context, location string, search []string) string {
	if filepath.IsAbs(location) {
		if ctx.Err() == nil {
			if _, err := os.Stat(location); err == nil {
				return location
			}
		}
		return ""
	}
	if strings.ContainsAny(location, `/\`) || !filepath.IsLocal(location) || location == "." {
		return ""
	}
	for _, dir := range search {
		if ctx.Err() != nil {
			break
		}
		if path, err := exec.LookPath(filepath.Join(dir, location)); err == nil {
			return path
		}
	}
	return ""
}
