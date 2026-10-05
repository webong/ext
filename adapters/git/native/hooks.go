package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"unicode/utf8"
)

const (
	beginMarker = "# ctx-git: begin\n"
	endMarker   = "# ctx-git: end\n"
	ownerMarker = "# ctx-git: generated\n"
	maxHookSize = 1 << 20
)

var hookName = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

type hookOptions struct {
	action, name, directory string
	command                 []string
	explicitDirectory       bool
}

func hooks(git string, args []string, out io.Writer) error {
	opts, err := parseHookOptions(args)
	if err != nil {
		return err
	}
	root, err := gitOutput(git, "rev-parse", "--show-toplevel")
	if err != nil || root == "" {
		return errors.New("hooks require a non-bare Git repository")
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return err
	}
	configured, configErr := gitOutput(git, "-C", root, "config", "--get", "core.hooksPath")
	if configErr != nil && !isExitCode(configErr, 1) {
		return fmt.Errorf("read core.hooksPath: %w", configErr)
	}
	active := configured
	if active == "" {
		active, err = gitOutput(git, "-C", root, "rev-parse", "--git-path", "hooks")
		if err != nil {
			return fmt.Errorf("find Git hooks: %w", err)
		}
	}
	active = absoluteFrom(root, active)
	target := active
	if opts.explicitDirectory {
		target = absoluteFrom(root, opts.directory)
	}
	if opts.action == "status" {
		fmt.Fprintf(out, "repository: %s\nactive hooks: %s\ntarget hooks: %s\n", root, active, target)
		if opts.name != "" {
			return printHookStatus(target, opts.name, out)
		}
		entries, err := os.ReadDir(target)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if hookName.MatchString(entry.Name()) {
				if err := printHookStatus(target, entry.Name(), out); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := safeHookPath(root, target); err != nil {
		return err
	}
	if opts.action == "install" {
		if configured != "" && !opts.explicitDirectory {
			return errors.New("core.hooksPath is configured; choose the editable hook directory with --directory")
		}
		return installHook(target, opts, out)
	}
	return removeHook(target, opts.name, out)
}

func printHookStatus(dir, name string, out io.Writer) error {
	data, _, err := readHook(filepath.Join(dir, name))
	if errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(out, "%s: missing\n", name)
		return nil
	}
	if err != nil {
		return err
	}
	state := "other"
	if bytes.Contains(data, []byte(beginMarker)) {
		state = "ctx-managed"
	}
	fmt.Fprintf(out, "%s: %s\n", name, state)
	return nil
}

func parseHookOptions(args []string) (hookOptions, error) {
	var opts hookOptions
	if len(args) == 0 {
		return opts, errors.New("usage: ctx run git hooks {status|install|remove} [--directory DIR] [HOOK] [-- COMMAND ...]")
	}
	opts.action = args[0]
	if opts.action != "status" && opts.action != "install" && opts.action != "remove" {
		return opts, fmt.Errorf("unknown hooks action %q", opts.action)
	}
	for i := 1; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--directory" && i+1 < len(args):
			i++
			opts.directory, opts.explicitDirectory = args[i], true
		case strings.HasPrefix(arg, "--directory="):
			opts.directory, opts.explicitDirectory = strings.TrimPrefix(arg, "--directory="), true
		case arg == "--" && opts.action == "install":
			opts.command = args[i+1:]
			i = len(args)
		case strings.HasPrefix(arg, "-") || opts.name != "":
			return opts, fmt.Errorf("unexpected hooks argument %q", arg)
		default:
			opts.name = arg
		}
	}
	if opts.explicitDirectory && opts.directory == "" {
		return opts, errors.New("--directory needs a path")
	}
	if opts.name != "" && !hookName.MatchString(opts.name) {
		return opts, fmt.Errorf("invalid hook name %q", opts.name)
	}
	if opts.action != "status" && opts.name == "" {
		return opts, errors.New("hook name is required")
	}
	if opts.action == "install" && len(opts.command) == 0 {
		return opts, errors.New("install needs -- COMMAND [ARGS...]")
	}
	for _, arg := range opts.command {
		if strings.ContainsAny(arg, "\x00\r\n") {
			return opts, errors.New("hook command arguments cannot contain NUL or newlines")
		}
	}
	return opts, nil
}

func gitOutput(git string, args ...string) (string, error) {
	cmd := exec.Command(git, args...)
	output, err := cmd.Output()
	return strings.TrimSpace(string(output)), err
}

func isExitCode(err error, code int) bool {
	var exit *exec.ExitError
	return errors.As(err, &exit) && exit.ExitCode() == code
}

func absoluteFrom(root, path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(root, path)
}

func safeHookPath(root, target string) error {
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == "." {
		return errors.New("hook directory must be inside the repository")
	}
	current := root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("unsafe hook directory %s", current)
		}
	}
	return nil
}

func readHook(path string) ([]byte, os.FileMode, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, 0, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxHookSize {
		return nil, 0, fmt.Errorf("hook is not a small regular file: %s", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, err
	}
	if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		return nil, 0, fmt.Errorf("hook is not a UTF-8 text file: %s", path)
	}
	return data, info.Mode().Perm(), nil
}

func installHook(dir string, opts hookOptions, out io.Writer) error {
	path := filepath.Join(dir, opts.name)
	data, mode, err := readHook(path)
	newFile := errors.Is(err, os.ErrNotExist)
	if err != nil && !newFile {
		return err
	}
	if !newFile {
		if !opts.explicitDirectory && runtime.GOOS != "windows" && mode&0111 == 0 {
			return errors.New("existing hook is not executable")
		}
		if bytes.Contains(data, []byte(beginMarker)) || bytes.Contains(data, []byte(endMarker)) {
			return errors.New("hook already contains a CTX block; remove it before reinstalling")
		}
		if (bytes.HasPrefix(data, []byte("#!")) || !opts.explicitDirectory) && !shellShebang(data) {
			return errors.New("existing hook is not a shell script")
		}
	}
	var quoted []string
	for _, arg := range opts.command {
		quoted = append(quoted, "'"+strings.ReplaceAll(arg, "'", `'"'"'`)+"'")
	}
	block := beginMarker + strings.Join(quoted, " ") + " || exit $?\n" + endMarker
	if newFile {
		data = []byte("#!/bin/sh\n" + ownerMarker + block)
		mode = 0755
	} else {
		position := 0
		if bytes.HasPrefix(data, []byte("#!")) {
			position = bytes.IndexByte(data, '\n') + 1
			if position == 0 {
				return errors.New("hook shebang has no newline")
			}
		}
		data = append(append(append([]byte{}, data[:position]...), []byte(block)...), data[position:]...)
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	if err := writeHook(path, data, mode); err != nil {
		return err
	}
	fmt.Fprintf(out, "installed %s in %s\n", opts.name, dir)
	return nil
}

func shellShebang(data []byte) bool {
	line, _, _ := bytes.Cut(data, []byte("\n"))
	return bytes.HasPrefix(line, []byte("#!")) && (bytes.Contains(line, []byte("/sh")) || bytes.Contains(line, []byte("/bash")) || bytes.Contains(line, []byte("/zsh")))
}

func removeHook(dir, name string, out io.Writer) error {
	path := filepath.Join(dir, name)
	data, mode, err := readHook(path)
	if err != nil {
		return err
	}
	start := bytes.Index(data, []byte(beginMarker))
	end := bytes.Index(data, []byte(endMarker))
	if start < 0 || end < start || bytes.Count(data, []byte(beginMarker)) != 1 || bytes.Count(data, []byte(endMarker)) != 1 {
		return errors.New("hook does not contain one valid CTX block")
	}
	end += len(endMarker)
	if bytes.Equal(data, []byte("#!/bin/sh\n"+ownerMarker+string(data[start:end]))) {
		if err := os.Remove(path); err != nil {
			return err
		}
	} else {
		clean := append(append([]byte{}, data[:start]...), data[end:]...)
		if err := writeHook(path, clean, mode); err != nil {
			return err
		}
	}
	fmt.Fprintf(out, "removed %s from %s\n", name, dir)
	return nil
}

func writeHook(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".ctx-hook-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	if err := tmp.Chmod(mode); err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
