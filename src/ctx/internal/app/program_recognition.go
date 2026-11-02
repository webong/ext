package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/webong/ext/res/system/program"
)

// A recognition is the user's statement that a file was installed by an installer.
// Adapters only suggest where a program came from; nothing becomes a fact in the
// graph until an installer reports the file or the user recognises it here.

type recognition struct {
	RealPath  string `json:"realpath"`
	Installer string `json:"installer"`
}

type recognitionFile struct {
	Version    int           `json:"version"`
	Recognised []recognition `json:"recognised"`
}

func recognitionPath() string {
	return filepath.Join(configHomePath(), "programs.json")
}

func loadRecognitions() (recognitionFile, error) {
	empty := recognitionFile{Version: 1}
	path := recognitionPath()
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return empty, nil
	}
	if err != nil {
		return empty, err
	}
	if !info.Mode().IsRegular() {
		return empty, fmt.Errorf("program recognitions are not a regular file: %s", path)
	}
	file, err := os.Open(path)
	if err != nil {
		return empty, err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, 1<<20))
	decoder.DisallowUnknownFields()
	var loaded recognitionFile
	if err := decoder.Decode(&loaded); err != nil {
		return empty, fmt.Errorf("read program recognitions: %w", err)
	}
	if loaded.Version != 1 {
		return empty, fmt.Errorf("unsupported program recognitions version %d", loaded.Version)
	}
	for _, item := range loaded.Recognised {
		if !filepath.IsAbs(item.RealPath) || !managerInstanceName.MatchString(item.Installer) {
			return empty, fmt.Errorf("invalid program recognition for %q", item.RealPath)
		}
	}
	return loaded, nil
}

// readRecognised is the recognitions as discovery uses them. A file that cannot be
// read recognises nothing: discovery never fails because of it.
func readRecognised() []recognition {
	loaded, err := loadRecognitions()
	if err != nil {
		return nil
	}
	return loaded.Recognised
}

func saveRecognitions(file recognitionFile) error {
	path := recognitionPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	sort.Slice(file.Recognised, func(i, j int) bool {
		if file.Recognised[i].RealPath != file.Recognised[j].RealPath {
			return file.Recognised[i].RealPath < file.Recognised[j].RealPath
		}
		return file.Recognised[i].Installer < file.Recognised[j].Installer
	})
	if file.Recognised == nil {
		file.Recognised = []recognition{}
	}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".ctx-programs-")
	if err != nil {
		return err
	}
	defer os.Remove(temporary.Name())
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(append(data, '\n')); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return replaceManagerRegistry(temporary.Name(), path)
}

// applyRecognised adds the installers the user recognised to the candidates for
// the same real file. A recognition for a file that is not a candidate is kept in
// the file and does nothing.
func applyRecognised(candidates []program.Candidate, recognised []recognition) []program.Candidate {
	if len(recognised) == 0 {
		return candidates
	}
	byFile := map[string][]string{}
	for _, item := range recognised {
		byFile[item.RealPath] = append(byFile[item.RealPath], item.Installer)
	}
	out := make([]program.Candidate, len(candidates))
	for i, candidate := range candidates {
		out[i] = candidate
		installers := byFile[candidate.RealPath]
		if len(installers) == 0 {
			continue
		}
		merged := append([]string(nil), candidate.Installers...)
		for _, installer := range installers {
			found := false
			for _, existing := range merged {
				found = found || existing == installer
			}
			if !found {
				merged = append(merged, installer)
			}
		}
		sort.Strings(merged)
		out[i].Installers = merged
		out[i].Source = program.SourceInstaller
	}
	return out
}
