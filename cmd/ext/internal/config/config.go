// Package config reads ext's own settings file.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// File is the name of the settings file inside the ext home.
const File = "ext.json"

// Config is the content of ext.json.
type Config struct {
	// Sources pins a command to the source that runs it by default: "builtin" or
	// "package". A command without an entry follows the default (builtin first).
	Sources map[string]string `json:"sources,omitempty"`
}

// Load reads home/ext.json. A missing file is an empty config; a malformed one is
// an error, so a typo never silently changes which code runs.
func Load(home string) (Config, error) {
	path := filepath.Join(home, File)
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Config{}, nil
	}
	if err != nil {
		return Config{}, err
	}
	var c Config
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}
