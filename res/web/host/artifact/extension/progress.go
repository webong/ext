package extension

import (
	"encoding/json"
	"strings"
)

// ProgressPrefix begins every progress line an adapter writes to stderr while a
// long operation runs (such as a browser session). The rest of the line is one
// JSON InstallResult.
const ProgressPrefix = "browser management progress: "

// MaxProgressLineBytes bounds one progress line a host should parse.
const MaxProgressLineBytes = 1 << 20

// ProgressLine encodes a progress report as an adapter writes it, without the
// trailing newline.
func ProgressLine(result InstallResult) (string, error) {
	encoded, err := json.Marshal(result)
	if err != nil {
		return "", err
	}
	return ProgressPrefix + string(encoded), nil
}

// ParseProgress decodes one stderr line. ok is false for any other line, an
// oversize line, or a malformed report, so callers can pass every stderr line
// through it and treat the rest as diagnostics. A trailing newline is allowed.
func ParseProgress(line string) (result InstallResult, ok bool) {
	line = strings.TrimRight(line, "\r\n")
	if len(line) > MaxProgressLineBytes || !strings.HasPrefix(line, ProgressPrefix) {
		return InstallResult{}, false
	}
	if err := json.Unmarshal([]byte(line[len(ProgressPrefix):]), &result); err != nil {
		return InstallResult{}, false
	}
	return result, result.Status != ""
}
