package adapter

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type GoBuildOptions struct {
	Source string
	Output string
	OS     string
	Arch   string
}

// BuildGoPackage compiles a local Go adapter and creates a platform archive.
// Only adapter.toml, the resulting executable, and declared package_files are shipped.
func BuildGoPackage(options GoBuildOptions) (string, error) {
	source, err := filepath.Abs(options.Source)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(source)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("Go adapter source directory not found: %s", options.Source)
	}
	values, err := parseManifest(filepath.Join(source, "adapter.toml"))
	if err != nil {
		return "", err
	}
	name := values["name"]
	if !validName.MatchString(name) || reservedNames[name] || values["api_version"] != APIVersion {
		return "", errors.New("Go adapter needs a valid API 2.0 manifest and name")
	}
	goos, goarch := options.OS, options.Arch
	if goos == "" {
		goos = runtime.GOOS
	}
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	if !validTarget.MatchString(goos) || !validTarget.MatchString(goarch) {
		return "", errors.New("invalid Go build target")
	}
	executable := values["executable"]
	if goos == "windows" && values["executable_windows"] != "" {
		executable = values["executable_windows"]
	}
	if !validExecutableName(executable) || goos == "windows" && !strings.HasSuffix(strings.ToLower(executable), ".exe") {
		return "", errors.New("Go adapter manifest needs a target executable name (with .exe on Windows)")
	}
	entry := values["go_entry"]
	if entry == "" {
		entry = "."
	}
	if entry != "." && (!strings.HasPrefix(entry, "./") || !validPackagePath(strings.TrimPrefix(entry, "./"))) {
		return "", errors.New("go_entry must be . or a relative ./package path")
	}
	output := options.Output
	if output == "" {
		output = filepath.Join(source, "dist", fmt.Sprintf("%s-%s-%s.ctxadapter", name, goos, goarch))
	}
	output, err = filepath.Abs(output)
	if err != nil {
		return "", err
	}
	staging, err := os.MkdirTemp("", "ctx-adapter-build-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(staging)
	if err := copyBuildAsset(source, staging, "adapter.toml"); err != nil {
		return "", err
	}
	for _, asset := range splitList(values["package_files"]) {
		if !validPackagePath(asset) || asset == artifactMetadataName || asset == "adapter.toml" || asset == executable {
			return "", fmt.Errorf("invalid package_files entry %q", asset)
		}
		if err := copyBuildAsset(source, staging, asset); err != nil {
			return "", err
		}
	}
	if _, err := os.Lstat(filepath.Join(staging, executable)); err == nil {
		return "", fmt.Errorf("package_files must not include the built executable %s", executable)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "build", "-trimpath", "-o", filepath.Join(staging, executable), entry)
	command.Dir = source
	command.Env = setEnvironment(os.Environ(), "GOOS", goos)
	command.Env = setEnvironment(command.Env, "GOARCH", goarch)
	if (goos != runtime.GOOS || goarch != runtime.GOARCH) && os.Getenv("CGO_ENABLED") == "" {
		command.Env = setEnvironment(command.Env, "CGO_ENABLED", "0")
	}
	buildOutput, err := command.CombinedOutput()
	if err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("build Go adapter: %w", ctx.Err())
		}
		return "", fmt.Errorf("build Go adapter: %w\n%s", err, strings.TrimSpace(string(buildOutput)))
	}
	if _, err := LoadDirectoryForOS(staging, goos); err != nil {
		return "", fmt.Errorf("built adapter is invalid: %w", err)
	}
	if err := PackageDirectory(staging, output, goos, goarch); err != nil {
		return "", err
	}
	return output, nil
}

func copyBuildAsset(source, target, relative string) error {
	path := filepath.Join(source, filepath.FromSlash(relative))
	destination := filepath.Join(target, filepath.FromSlash(relative))
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("package asset %s: %w", relative, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("package asset %s is a symbolic link", relative)
	}
	if info.IsDir() {
		return copyDirectory(path, destination)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("package asset %s is not a regular file", relative)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return err
	}
	input, err := os.Open(path)
	if err != nil {
		return err
	}
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		input.Close()
		return err
	}
	_, copyErr := io.Copy(output, input)
	inputErr := input.Close()
	outputErr := output.Close()
	return errors.Join(copyErr, inputErr, outputErr)
}
