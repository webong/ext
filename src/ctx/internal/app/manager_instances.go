package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/webong/ext/ctx/internal/config"
)

var managerInstanceName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

type managerInstance struct {
	Name        string            `json:"name"`
	Virtualizer string            `json:"virtualizer,omitempty"`
	Machine     string            `json:"machine,omitempty"`
	Provider    string            `json:"provider"`
	Selection   string            `json:"selection"`
	Address     string            `json:"address,omitempty"`
	Command     string            `json:"command,omitempty"`
	PluginDir   string            `json:"plugin_dir,omitempty"`
	Plugins     map[string]string `json:"plugins,omitempty"`
}

func managerInstanceEnvironment(instance managerInstance) map[string]string {
	environment := map[string]string{"CTX_MANAGER_ADDRESS": instance.Address}
	if instance.Command != "" {
		environment["CTX_MANAGER_COMMAND"] = instance.Command
	}
	if instance.PluginDir != "" {
		environment["CTX_MANAGER_PLUGIN_DIR"] = instance.PluginDir
	}
	for name, path := range instance.Plugins {
		environment["CTX_MANAGER_PLUGIN_"+strings.ToUpper(name)] = path
	}
	return environment
}

type managerRegistry struct {
	Version   int               `json:"version"`
	Instances []managerInstance `json:"instances"`
}

func managerRegistryPath() string {
	return filepath.Join(configHomePath(), "managers.json")
}

func readManagerRegistry() (managerRegistry, error) {
	registry := managerRegistry{}
	path := managerRegistryPath()
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return managerRegistry{Version: 1}, nil
	}
	if err != nil {
		return registry, err
	}
	if !info.Mode().IsRegular() {
		return registry, fmt.Errorf("manager registry is not a regular file: %s", path)
	}
	file, err := os.Open(path)
	if err != nil {
		return registry, err
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&registry); err != nil {
		return registry, fmt.Errorf("read manager registry: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return registry, fmt.Errorf("manager registry has trailing data")
	}
	if registry.Version != 1 {
		return registry, fmt.Errorf("unsupported manager registry version %d", registry.Version)
	}
	seen := map[string]bool{}
	for _, instance := range registry.Instances {
		if err := validateManagerInstance(instance); err != nil {
			return registry, err
		}
		if seen[instance.Name] {
			return registry, fmt.Errorf("duplicate manager instance %s", instance.Name)
		}
		seen[instance.Name] = true
	}
	return registry, nil
}

func validateManagerInstance(instance managerInstance) error {
	if !managerInstanceName.MatchString(instance.Name) ||
		(instance.Virtualizer != "" && !managerInstanceName.MatchString(instance.Virtualizer)) ||
		(instance.Machine != "" && !managerInstanceName.MatchString(instance.Machine)) {
		return fmt.Errorf("invalid manager instance name, virtualizer, or machine")
	}
	if instance.Provider == "" || instance.Selection == "" || strings.ContainsAny(instance.Provider+instance.Selection+instance.Address, "\x00\r\n") {
		return fmt.Errorf("manager instance %s needs a provider and selection without control characters", instance.Name)
	}
	if instance.Command != "" {
		if !filepath.IsAbs(instance.Command) {
			return fmt.Errorf("manager instance %s command path must be absolute", instance.Name)
		}
	}
	if instance.PluginDir != "" {
		if !filepath.IsAbs(instance.PluginDir) {
			return fmt.Errorf("manager instance %s plugin directory must be absolute", instance.Name)
		}
	}
	if len(instance.Plugins) > 0 && instance.Provider != "docker" {
		return fmt.Errorf("manager instance %s plugin paths currently require the docker provider", instance.Name)
	}
	for name, path := range instance.Plugins {
		if name != "buildx" && name != "compose" {
			return fmt.Errorf("manager instance %s has unsupported Docker plugin %s", instance.Name, name)
		}
		if !filepath.IsAbs(path) {
			return fmt.Errorf("manager instance %s plugin %s path must be absolute", instance.Name, name)
		}
	}
	if instance.PluginDir != "" && instance.Provider != "docker" {
		return fmt.Errorf("manager instance %s plugin directory currently requires the docker provider", instance.Name)
	}
	return nil
}

func validateManagerToolchain(instance managerInstance) error {
	if instance.Command != "" {
		if err := validateManagerExecutable(instance.Command); err != nil {
			return fmt.Errorf("command: %w", err)
		}
	}
	if instance.PluginDir != "" {
		info, err := os.Stat(instance.PluginDir)
		if err != nil || !info.IsDir() {
			return fmt.Errorf("plugin directory is unavailable: %s", instance.PluginDir)
		}
	}
	for name, path := range instance.Plugins {
		if err := validateManagerExecutable(path); err != nil {
			return fmt.Errorf("plugin %s: %w", name, err)
		}
	}
	return nil
}

func validateManagerExecutable(path string) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("path must be absolute: %s", path)
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("executable is unavailable: %s", path)
	}
	if info.Mode()&0o111 == 0 && filepath.Ext(path) != ".exe" && filepath.Ext(path) != ".cmd" && filepath.Ext(path) != ".bat" {
		return fmt.Errorf("path is not executable: %s", path)
	}
	return nil
}

func writeManagerRegistry(registry managerRegistry) error {
	path := managerRegistryPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("manager registry is not a regular file: %s", path)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	sort.Slice(registry.Instances, func(i, j int) bool { return registry.Instances[i].Name < registry.Instances[j].Name })
	data, err := json.MarshalIndent(registry, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	temporary, err := os.CreateTemp(filepath.Dir(path), ".ctx-managers-")
	if err != nil {
		return err
	}
	defer os.Remove(temporary.Name())
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
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

func updateManagerRegistry(change func(*managerRegistry) error) error {
	path := managerRegistryPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	lockPath := path + ".lock"
	deadline := time.Now().Add(2 * time.Second)
	for {
		lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_, _ = fmt.Fprintf(lock, "%d\n", os.Getpid())
			_ = lock.Close()
			defer os.Remove(lockPath)
			registry, err := readManagerRegistry()
			if err != nil {
				return err
			}
			if err := change(&registry); err != nil {
				return err
			}
			return writeManagerRegistry(registry)
		}
		if !errors.Is(err, os.ErrExist) {
			return err
		}
		if info, statErr := os.Stat(lockPath); statErr == nil && time.Since(info.ModTime()) > 30*time.Second {
			_ = os.Remove(lockPath)
			continue
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("manager registry is busy")
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func managerCommand(resolver *config.Resolver, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "ctx: manager requires add, ls, show, apps, app, doctor, or remove")
		return 2
	}
	registry, err := readManagerRegistry()
	if err != nil {
		return reportError(stderr, err)
	}
	switch args[0] {
	case "apps":
		if len(args) != 1 {
			fmt.Fprintln(stderr, "ctx: manager apps takes no arguments")
			return 2
		}
		store := adapterStore()
		installed, err := store.List()
		if err != nil {
			return reportError(stderr, err)
		}
		for _, candidate := range installed {
			if !managerAppAdapter(candidate) {
				continue
			}
			if trusted, err := store.IsTrusted(candidate); err == nil && trusted {
				fmt.Fprintln(stdout, candidate.Manifest.Name)
			}
		}
		return 0
	case "ls", "list":
		if len(args) != 1 {
			fmt.Fprintln(stderr, "ctx: manager ls takes no arguments")
			return 2
		}
		inventory, err := resolvedMachineInventory(resolver)
		if err != nil {
			return reportError(stderr, err)
		}
		printDiscoveredManagers(inventory, stdout)
		seen := map[string]bool{}
		for _, instance := range registry.Instances {
			if _, err := inventory.Find(instance.Provider, instance.Selection); err == nil {
				seen[instance.Provider+"\x00"+instance.Selection] = true
			}
		}
		for _, instance := range registry.Instances {
			state := "unavailable"
			if seen[instance.Provider+"\x00"+instance.Selection] {
				state = "discovered"
			}
			fmt.Fprintf(stdout, "@%s\t%s\t%s:%s\t%s", instance.Name, managerDescription(instance), instance.Provider, instance.Selection, state)
			if instance.Address != "" {
				fmt.Fprint(stdout, " (pinned address)")
			}
			fmt.Fprintln(stdout)
		}
		return 0
	case "show":
		if len(args) != 2 {
			fmt.Fprintln(stderr, "ctx: manager show needs an instance name")
			return 2
		}
		instance, ok := findManagerInstance(registry, strings.TrimPrefix(args[1], "@"))
		if !ok {
			return reportErrorCode(stderr, fmt.Errorf("unknown manager instance %s", args[1]), 2)
		}
		fmt.Fprintf(stdout, "name:        @%s\n", instance.Name)
		if instance.Virtualizer != "" {
			fmt.Fprintf(stdout, "virtualizer: %s\n", instance.Virtualizer)
		}
		if instance.Machine != "" {
			fmt.Fprintf(stdout, "machine:     %s\n", instance.Machine)
		}
		fmt.Fprintf(stdout, "provider:    %s\nselection:   %s\n", instance.Provider, instance.Selection)
		if instance.Address != "" {
			fmt.Fprintf(stdout, "address:     %s\n", instance.Address)
		}
		if instance.Command != "" {
			fmt.Fprintf(stdout, "command:     %s\n", instance.Command)
		}
		if instance.PluginDir != "" {
			fmt.Fprintf(stdout, "plugin dir:  %s\n", instance.PluginDir)
		}
		for _, name := range []string{"buildx", "compose"} {
			if path := instance.Plugins[name]; path != "" {
				fmt.Fprintf(stdout, "plugin %s: %s\n", name, path)
			}
		}
		return 0
	case "add":
		if len(args) < 2 || !managerInstanceName.MatchString(args[1]) {
			fmt.Fprintln(stderr, "ctx: manager add needs a valid instance name")
			return 2
		}
		instance := managerInstance{Name: args[1], Plugins: map[string]string{}}
		seen := map[string]bool{}
		offline := false
		for index := 2; index < len(args); {
			if args[index] == "--offline" {
				if offline {
					fmt.Fprintln(stderr, "ctx: --offline may be specified only once")
					return 2
				}
				offline = true
				index++
				continue
			}
			if index+1 >= len(args) || args[index+1] == "" || (seen[args[index]] && args[index] != "--plugin") {
				fmt.Fprintln(stderr, "ctx: manager add needs distinct option/value pairs")
				return 2
			}
			seen[args[index]] = true
			switch args[index] {
			case "--virtualizer":
				instance.Virtualizer = args[index+1]
			case "--machine":
				instance.Machine = args[index+1]
			case "--provider":
				instance.Provider = args[index+1]
			case "--selection":
				instance.Selection = args[index+1]
			case "--address":
				instance.Address = args[index+1]
			case "--command":
				instance.Command = args[index+1]
			case "--plugin-dir":
				instance.PluginDir = args[index+1]
			case "--plugin":
				name, path, ok := strings.Cut(args[index+1], "=")
				if !ok || name == "" || path == "" || instance.Plugins[name] != "" {
					fmt.Fprintln(stderr, "ctx: --plugin needs a distinct name=/absolute/path")
					return 2
				}
				instance.Plugins[name] = path
			default:
				fmt.Fprintf(stderr, "ctx: unknown manager option %s\n", args[index])
				return 2
			}
			index += 2
		}
		if err := validateManagerInstance(instance); err != nil {
			return reportErrorCode(stderr, err, 2)
		}
		if err := validateManagerToolchain(instance); err != nil {
			return reportErrorCode(stderr, err, 2)
		}
		if _, ok := findManagerInstance(registry, instance.Name); ok {
			return reportErrorCode(stderr, fmt.Errorf("manager instance %s already exists", instance.Name), 2)
		}
		provider, err := managerProvider(instance.Provider)
		if err != nil {
			return reportErrorCode(stderr, err, 2)
		}
		candidate := endpoint{Provider: provider, Name: instance.Selection, Instance: &instance}
		if !offline {
			if code := validateEndpoint(resolver, candidate, stderr); code != 0 {
				return code
			}
		}
		if err := updateManagerRegistry(func(current *managerRegistry) error {
			if _, exists := findManagerInstance(*current, instance.Name); exists {
				return fmt.Errorf("manager instance %s already exists", instance.Name)
			}
			current.Instances = append(current.Instances, instance)
			return nil
		}); err != nil {
			return reportError(stderr, err)
		}
		if _, err := scanMachineInventoryInGraph(resolver); err != nil {
			fmt.Fprintf(stderr, "ctx: warning: could not update manager graph: %v\n", err)
		}
		fmt.Fprintf(stdout, "registered @%s (%s via %s:%s)\n", instance.Name, managerDescription(instance), instance.Provider, instance.Selection)
		return 0
	case "doctor":
		if len(args) > 2 {
			fmt.Fprintln(stderr, "ctx: manager doctor accepts at most one instance name")
			return 2
		}
		name := ""
		if len(args) == 2 {
			name = strings.TrimPrefix(args[1], "@")
			if _, ok := findManagerInstance(registry, name); !ok {
				candidate, err := adapterStore().Load(name)
				if err != nil || !managerAppAdapter(candidate) {
					return reportErrorCode(stderr, fmt.Errorf("unknown manager instance or app %s", args[1]), 2)
				}
			}
		}
		return doctorManagers(resolver, registry, name, stdout, stderr)
	case "app":
		if len(args) != 3 {
			fmt.Fprintln(stderr, "ctx: manager app needs an adapter and status, start, stop, or doctor")
			return 2
		}
		return managerAppCommand(resolver, args[1], args[2], stdout, stderr)
	case "remove":
		if len(args) != 2 {
			fmt.Fprintln(stderr, "ctx: manager remove needs an instance name")
			return 2
		}
		name := strings.TrimPrefix(args[1], "@")
		if err := updateManagerRegistry(func(current *managerRegistry) error {
			for index, instance := range current.Instances {
				if instance.Name == name {
					current.Instances = append(current.Instances[:index], current.Instances[index+1:]...)
					return nil
				}
			}
			return fmt.Errorf("unknown manager instance %s", args[1])
		}); err != nil {
			return reportError(stderr, err)
		}
		if _, err := scanMachineInventoryInGraph(resolver); err != nil {
			fmt.Fprintf(stderr, "ctx: warning: could not update manager graph: %v\n", err)
		}
		fmt.Fprintf(stdout, "removed @%s\n", name)
		return 0
	default:
		fmt.Fprintln(stderr, "ctx: manager requires add, ls, show, apps, app, doctor, or remove")
		return 2
	}
}

func findManagerInstance(registry managerRegistry, name string) (managerInstance, bool) {
	for _, instance := range registry.Instances {
		if instance.Name == name {
			return instance, true
		}
	}
	return managerInstance{}, false
}

func managerDescription(instance managerInstance) string {
	if instance.Virtualizer == "" {
		return instance.Provider
	}
	if instance.Machine != "" {
		return instance.Virtualizer + "/" + instance.Machine
	}
	return instance.Virtualizer
}
