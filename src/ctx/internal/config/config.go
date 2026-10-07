package config

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Profile struct {
	Values map[string]string
	Env    map[string]string
}

type File struct {
	Defaults map[string]string
	Projects map[string]map[string]string
	Profiles map[string]*Profile
}

func emptyFile() *File {
	return &File{
		Defaults: map[string]string{},
		Projects: map[string]map[string]string{},
		Profiles: map[string]*Profile{},
	}
}

func Load(path string) (*File, error) {
	f := emptyFile()
	input, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return f, nil
	}
	if err != nil {
		return nil, err
	}
	defer input.Close()

	sectionKind := "root"
	sectionName := ""
	scanner := bufio.NewScanner(input)
	for lineNumber := 1; scanner.Scan(); lineNumber++ {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			kind, name, ok := parseSection(line)
			if !ok {
				sectionKind, sectionName = "ignored", ""
				continue
			}
			sectionKind, sectionName = kind, name
			continue
		}
		key, value, ok := parseAssignment(line)
		if !ok {
			continue
		}
		switch sectionKind {
		case "root":
			f.Defaults[key] = value
		case "project":
			if f.Projects[sectionName] == nil {
				f.Projects[sectionName] = map[string]string{}
			}
			f.Projects[sectionName][key] = value
		case "profile":
			profile := ensureProfile(f, sectionName)
			profile.Values[key] = value
		case "profile_env":
			profile := ensureProfile(f, sectionName)
			profile.Env[key] = value
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return f, nil
}

func ensureProfile(f *File, name string) *Profile {
	if f.Profiles[name] == nil {
		f.Profiles[name] = &Profile{Values: map[string]string{}, Env: map[string]string{}}
	}
	return f.Profiles[name]
}

func parseSection(line string) (string, string, bool) {
	if !strings.HasSuffix(line, "]") {
		return "", "", false
	}
	body := strings.TrimSuffix(strings.TrimPrefix(line, "["), "]")
	for _, candidate := range []struct {
		prefix string
		kind   string
		suffix string
	}{
		{`profiles."`, "profile_env", `".env`},
		{`profiles."`, "profile", `"`},
		{`projects."`, "project", `"`},
	} {
		if strings.HasPrefix(body, candidate.prefix) && strings.HasSuffix(body, candidate.suffix) {
			name := strings.TrimSuffix(strings.TrimPrefix(body, candidate.prefix), candidate.suffix)
			if name != "" {
				return candidate.kind, name, true
			}
		}
	}
	return "", "", false
}

func parseAssignment(line string) (string, string, bool) {
	parts := strings.SplitN(line, "=", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	key := strings.TrimSpace(parts[0])
	raw := strings.TrimSpace(parts[1])
	if key == "" || len(raw) < 2 || raw[0] != '"' {
		return "", "", false
	}
	value, err := strconv.Unquote(raw)
	if err != nil {
		return "", "", false
	}
	return key, value, true
}

func ReadFlat(path string) (map[string]string, error) {
	values := map[string]string{}
	input, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return values, nil
	}
	if err != nil {
		return nil, err
	}
	defer input.Close()
	scanner := bufio.NewScanner(input)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if key, value, ok := parseAssignment(line); ok {
			values[key] = value
		}
	}
	return values, scanner.Err()
}

type Resolution struct {
	Value  string
	Source string
}

type Resolver struct {
	WorkingDir string
	HomeDir    string
	ConfigPath string
	Config     *File
}

func NewResolver(workingDir, homeDir, configPath string) (*Resolver, error) {
	parsed, err := Load(configPath)
	if err != nil {
		return nil, err
	}
	return &Resolver{WorkingDir: filepath.Clean(workingDir), HomeDir: filepath.Clean(homeDir), ConfigPath: configPath, Config: parsed}, nil
}

func (r *Resolver) Resolve(key string) (Resolution, error) {
	for dir := r.WorkingDir; ; dir = filepath.Dir(dir) {
		localPath := filepath.Join(dir, ".ctx")
		local, err := ReadFlat(localPath)
		if err != nil {
			return Resolution{}, err
		}
		if value := local[key]; value != "" {
			return Resolution{Value: value, Source: localPath}, nil
		}
		if key != "profile" {
			if result, ok := r.fromProfile(local["profile"], key, "profile"); ok {
				return result, nil
			}
		}
		if project := r.Config.Projects[dir]; project != nil {
			if value := project[key]; value != "" {
				return Resolution{Value: value, Source: r.ConfigPath + " project map"}, nil
			}
			if key != "profile" {
				if result, ok := r.fromProfile(project["profile"], key, "project profile"); ok {
					return result, nil
				}
			}
		}
		parent := filepath.Dir(dir)
		if dir == r.HomeDir || parent == dir {
			break
		}
	}
	if value := r.Config.Defaults[key+"_default"]; value != "" {
		return Resolution{Value: value, Source: r.ConfigPath + " default"}, nil
	}
	return Resolution{Source: key + " default"}, nil
}

func (r *Resolver) fromProfile(name, key, source string) (Resolution, bool) {
	if name == "" || r.Config.Profiles[name] == nil {
		return Resolution{}, false
	}
	if value := r.Config.Profiles[name].Values[key]; value != "" {
		return Resolution{Value: value, Source: r.ConfigPath + " " + source + " " + name}, true
	}
	return Resolution{}, false
}

func (r *Resolver) ActiveProfile() (string, *Profile, error) {
	resolved, err := r.Resolve("profile")
	if err != nil || resolved.Value == "" {
		return "", nil, err
	}
	return resolved.Value, r.Config.Profiles[resolved.Value], nil
}
