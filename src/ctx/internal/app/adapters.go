package app

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/webong/ctx/src/ctx/internal/config"
	"github.com/webong/ctx/src/ctx/internal/launch"
	modpkg "github.com/webong/ctx/src/ctx/internal/mod"
)

func adapterStore() *modpkg.Store {
	home := os.Getenv("CTX_ADAPTER_HOME")
	if home == "" {
		home = filepath.Join(configHomePath(), "adapters")
	}
	return modpkg.NewStore(home)
}

func sortedDependencySpaces(dependencies map[string]modpkg.Dependency) []string {
	spaces := make([]string, 0, len(dependencies))
	for space := range dependencies {
		spaces = append(spaces, space)
	}
	sort.Strings(spaces)
	return spaces
}

func stripDependencyEnvironment(values []string) []string {
	filtered := values[:0]
	for _, value := range values {
		if !strings.HasPrefix(strings.ToUpper(value), "CTX_DEPENDENCY_") {
			filtered = append(filtered, value)
		}
	}
	return filtered
}

func adapterCommand(resolver *config.Resolver, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "ctx: adapter requires ls, available, add, refresh, inspect, build, pack, index, install, trust, test, doctor, or remove")
		return 2
	}
	store := adapterStore()
	switch args[0] {
	case "ls", "list":
		if len(args) > 2 {
			fmt.Fprintln(stderr, "ctx: adapter ls accepts an optional runtime or surface")
			return 2
		}
		filter := ""
		if len(args) == 2 {
			filter = args[1]
			if filter != "computer" && filter != "manager" && filter != "browser" && filter != "shell" && filter != "web" {
				fmt.Fprintf(stderr, "ctx: unknown runtime or surface %s\n", args[1])
				return 2
			}
		}
		installed, err := store.List()
		if err != nil {
			return reportError(stderr, err)
		}
		for _, candidate := range installed {
			if filter != "" && !candidate.IsRuntime(filter) && !candidate.SupportsSurface(filter) {
				continue
			}
			state := "untrusted"
			if trusted, err := store.IsTrusted(candidate); err == nil && trusted {
				state = "trusted"
			}
			fmt.Fprintf(stdout, "%-16s %-10s %s\n", candidate.Manifest.Name, state, candidate.Manifest.Description)
		}
		return 0
	case "inspect":
		if len(args) != 2 {
			fmt.Fprintln(stderr, "ctx: adapter inspect needs a name")
			return 2
		}
		candidate, err := store.Load(args[1])
		if err != nil {
			return reportError(stderr, err)
		}
		state := "untrusted"
		if trusted, err := store.IsTrusted(candidate); err == nil && trusted {
			state = "trusted"
		}
		fmt.Fprintf(stdout, "name:         %s\n", candidate.Manifest.Name)
		fmt.Fprintf(stdout, "display name: %s\n", candidate.DisplayLabel())
		fmt.Fprintf(stdout, "api:          %s\n", candidate.Manifest.APIVersion)
		fmt.Fprintf(stdout, "runtime:      %s\n", candidate.Manifest.Runtime)
		fmt.Fprintf(stdout, "surfaces:     %s\n", strings.Join(candidate.Manifest.Surfaces, ","))
		fmt.Fprintf(stdout, "state:        %s\n", state)
		fmt.Fprintf(stdout, "selector:     %s\n", candidate.Manifest.SelectorKey)
		fmt.Fprintf(stdout, "commands:     %s\n", strings.Join(candidate.Manifest.Commands, ","))
		if candidate.Manifest.SelfContained {
			fmt.Fprintln(stdout, "self-contained: true")
		}
		if len(candidate.Manifest.ComputerCommands) > 0 {
			fmt.Fprintf(stdout, "computer commands: %s\n", strings.Join(candidate.Manifest.ComputerCommands, ","))
		}
		if len(candidate.Manifest.ComputerCapabilities) > 0 {
			fmt.Fprintf(stdout, "computer capabilities: %s\n", strings.Join(candidate.Manifest.ComputerCapabilities, ","))
		}
		fmt.Fprintf(stdout, "capabilities: %s\n", strings.Join(candidate.Manifest.Capabilities, ","))
		if len(candidate.Manifest.Supports) > 0 {
			fmt.Fprintf(stdout, "supports:     %s\n", strings.Join(candidate.Manifest.Supports, ","))
		}
		if len(candidate.Manifest.ShareSpaces) > 0 {
			fmt.Fprintf(stdout, "share spaces: %s\n", strings.Join(candidate.Manifest.ShareSpaces, ","))
		}
		if len(candidate.Manifest.Dependencies) > 0 {
			for _, space := range sortedDependencySpaces(candidate.Manifest.Dependencies) {
				dependency := candidate.Manifest.Dependencies[space]
				fmt.Fprintf(stdout, "dependency:   %s:%s@%s\n", space, dependency.Adapter, dependency.APIVersion)
			}
		}
		if len(candidate.Manifest.OverrideEnv) > 0 {
			fmt.Fprintf(stdout, "override env: %s\n", strings.Join(candidate.Manifest.OverrideEnv, ","))
		}
		if candidate.IsRuntime("manager") {
			fmt.Fprintf(stdout, "default:      %t\n", candidate.Manifest.DefaultProvider)
		}
		fmt.Fprintf(stdout, "executable:   %s\n", candidate.ExecutablePath())
		fmt.Fprintf(stdout, "description:  %s\n", candidate.Manifest.Description)
		return 0
	case "available":
		if len(args) != 1 {
			fmt.Fprintln(stderr, "ctx: adapter available takes no arguments")
			return 2
		}
		available, err := catalogStore().List()
		if err != nil {
			return reportError(stderr, err)
		}
		installed := installedAdapterNames()
		for _, candidate := range available {
			state := "available"
			if installed[candidate.Manifest.Name] {
				state = "installed"
			}
			fmt.Fprintf(stdout, "%-16s %-12s %-10s %s\n", candidate.Manifest.Name, candidate.Manifest.Runtime, state, candidate.Manifest.Description)
		}
		return 0
	case "add":
		if len(args) < 2 {
			fmt.Fprintln(stderr, "ctx: adapter add needs one or more catalog names")
			return 2
		}
		for _, name := range args[1:] {
			if _, err := addCatalogAdapter(name); err != nil {
				return reportError(stderr, err)
			}
			fmt.Fprintf(stdout, "installed adapter %s\n", name)
		}
		return 0
	case "refresh":
		if len(args) != 1 {
			fmt.Fprintln(stderr, "ctx: adapter refresh takes no arguments")
			return 2
		}
		if err := refreshCatalogAdapters(stdout); err != nil {
			return reportError(stderr, err)
		}
		return 0
	case "install":
		if len(args) != 2 && (len(args) != 4 || args[2] != "--sha256") {
			fmt.Fprintln(stderr, "ctx: adapter install needs a directory or archive, optionally followed by --sha256 <digest>")
			return 2
		}
		digest := ""
		if len(args) == 4 {
			digest = args[3]
		}
		installed, err := store.InstallSource(args[1], digest)
		if err != nil {
			return reportError(stderr, err)
		}
		fmt.Fprintf(stdout, "installed adapter %s in %s (untrusted)\n", installed.Manifest.Name, installed.Directory)
		fmt.Fprintf(stdout, "review it, then run: ctx adapter trust %s\n", installed.Manifest.Name)
		return 0
	case "build", "pack":
		if len(args) < 2 {
			fmt.Fprintf(stderr, "ctx: adapter %s needs a source directory\n", args[0])
			return 2
		}
		output := ""
		goos, goarch := runtime.GOOS, runtime.GOARCH
		for index := 2; index < len(args); index += 2 {
			if index+1 >= len(args) {
				fmt.Fprintln(stderr, "ctx: adapter build/pack option needs a value")
				return 2
			}
			switch args[index] {
			case "--output":
				output = args[index+1]
			case "--os":
				goos = args[index+1]
			case "--arch":
				goarch = args[index+1]
			default:
				fmt.Fprintf(stderr, "ctx: unknown adapter build/pack option %s\n", args[index])
				return 2
			}
		}
		if args[0] == "build" {
			built, err := modpkg.BuildGoPackage(modpkg.GoBuildOptions{Source: args[1], Output: output, OS: goos, Arch: goarch})
			if err != nil {
				return reportError(stderr, err)
			}
			fmt.Fprintf(stdout, "built adapter package %s\n", built)
			return 0
		}
		candidate, err := modpkg.LoadDirectoryForOS(args[1], goos)
		if err != nil {
			return reportError(stderr, err)
		}
		if output == "" {
			output = filepath.Join(filepath.Dir(candidate.Directory), fmt.Sprintf("%s-%s-%s.ctxadapter", candidate.Manifest.Name, goos, goarch))
		}
		if err := modpkg.PackageDirectory(args[1], output, goos, goarch); err != nil {
			return reportError(stderr, err)
		}
		fmt.Fprintf(stdout, "packed adapter %s in %s\n", candidate.Manifest.Name, output)
		return 0
	case "index":
		if len(args) < 3 {
			fmt.Fprintln(stderr, "ctx: adapter index needs an output path and one or more archives")
			return 2
		}
		if err := modpkg.WriteIndex(args[1], args[2:]); err != nil {
			return reportError(stderr, err)
		}
		digest, err := modpkg.FileSHA256(args[1])
		if err != nil {
			return reportError(stderr, err)
		}
		fmt.Fprintf(stdout, "wrote adapter index %s\nsha256: %s\n", args[1], digest)
		return 0
	case "trust":
		if len(args) != 2 {
			fmt.Fprintln(stderr, "ctx: adapter trust needs a name")
			return 2
		}
		candidate, err := store.Load(args[1])
		if err != nil {
			return reportError(stderr, err)
		}
		if err := checkShimConflicts(candidate); err != nil {
			return reportError(stderr, err)
		}
		if err := store.Trust(candidate); err != nil {
			return reportError(stderr, err)
		}
		if err := installAdapterShims(candidate); err != nil {
			return reportError(stderr, err)
		}
		fmt.Fprintf(stdout, "trusted adapter %s\n", candidate.Manifest.Name)
		return 0
	case "test":
		if len(args) != 2 {
			fmt.Fprintln(stderr, "ctx: adapter test needs an adapter directory")
			return 2
		}
		candidate, err := modpkg.LoadDirectory(args[1])
		if err != nil {
			return reportError(stderr, err)
		}
		invocation, err := candidate.Command(modpkg.Invocation{Operation: "doctor", Project: currentDirectory()})
		if err != nil {
			return reportError(stderr, err)
		}
		if code := runPrepared(invocation, stdout, stderr); code != 0 {
			return code
		}
		fmt.Fprintf(stdout, "adapter %s satisfies ctx adapter API v%s\n", candidate.Manifest.Name, candidate.Manifest.APIVersion)
		return 0
	case "doctor":
		if len(args) != 2 {
			fmt.Fprintln(stderr, "ctx: adapter doctor needs a name")
			return 2
		}
		candidate, err := store.Load(args[1])
		if err != nil {
			return reportError(stderr, err)
		}
		selection, err := adapterSelection(resolver, candidate)
		if err != nil {
			return reportError(stderr, err)
		}
		return invokeAdapter(resolver, candidate, "doctor", selection, nil, "", stdout, stderr)
	case "remove":
		force := len(args) == 3 && args[2] == "--force"
		if len(args) != 2 && !force {
			fmt.Fprintln(stderr, "ctx: adapter remove needs a name, optionally followed by --force")
			return 2
		}
		candidate, err := store.Load(args[1])
		if err != nil {
			return reportError(stderr, err)
		}
		if !force {
			installed, err := store.List()
			if err != nil {
				return reportError(stderr, err)
			}
			for _, dependent := range installed {
				for _, dependency := range dependent.Manifest.Dependencies {
					if dependency.Adapter == candidate.Manifest.Name {
						return reportError(stderr, fmt.Errorf("adapter %s is required by %s; remove the dependent first or pass --force", candidate.Manifest.Name, dependent.Manifest.Name))
					}
				}
			}
		}
		if err := removeAdapterShims(candidate); err != nil {
			return reportError(stderr, err)
		}
		if err := store.Remove(args[1]); err != nil {
			return reportError(stderr, err)
		}
		fmt.Fprintf(stdout, "removed adapter %s\n", args[1])
		return 0
	default:
		fmt.Fprintln(stderr, "ctx: adapter requires ls, available, add, refresh, inspect, build, pack, index, install, trust, test, doctor, or remove")
		return 2
	}
}

func runAdapterTool(resolver *config.Resolver, tool string, args []string, stdout, stderr io.Writer) int {
	installed, err := adapterStore().List()
	if err != nil {
		return reportError(stderr, err)
	}
	var matched *modpkg.Adapter
	for _, candidate := range installed {
		commandMatch := candidate.HasCommand(tool) || candidate.HasComputerCommand(tool)
		if !candidate.SupportsSurface("shell") || !candidate.HasCapability("run") || (candidate.Manifest.Name != tool && !commandMatch) {
			continue
		}
		if matched != nil {
			fmt.Fprintf(stderr, "ctx: command %s is claimed by both %s and %s\n", tool, matched.Manifest.Name, candidate.Manifest.Name)
			return 1
		}
		matched = candidate
	}
	if matched == nil {
		fmt.Fprintf(stderr, "ctx: no adapter for %s\n", tool)
		return 2
	}
	selection, err := adapterSelection(resolver, matched)
	if err != nil {
		return reportError(stderr, err)
	}
	if selection == "" && !matched.IsRuntime("manager") && !matched.IsComputerEndpoint() {
		fmt.Fprintf(stderr, "ctx: no %s selection; run ctx set %s <name>\n", matched.Manifest.Name, matched.Manifest.Name)
		return 1
	}
	return invokeAdapter(resolver, matched, "run", selection, args, tool, stdout, stderr)
}

func listContexts(resolver *config.Resolver, args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "ctx: ls needs one selector")
		return 2
	}
	selector := args[0]
	switch selector {
	case "browser":
		return listBrowsers(resolver, stdout, stderr)
	case "manager":
		return listManagers(resolver, stdout, stderr)
	case "computer":
		inventory, err := resolvedMachineInventory(resolver)
		if err != nil {
			return reportError(stderr, err)
		}
		found := false
		for _, candidate := range inventory.Contexts {
			if candidate.Runtime == "computer" && candidate.Selection != "" {
				fmt.Fprintf(stdout, "%s:%s\n", candidate.Adapter, candidate.Selection)
				found = true
			}
		}
		if found {
			return 0
		}
		return listProviderFamily(resolver, "computer", true, stdout, stderr)
	}
	candidate, err := adapterStore().Load(selector)
	if err != nil || !candidate.IsSelectable() || (!candidate.HasCapability("list") && !candidate.HasCapability("observe")) {
		fmt.Fprintf(stderr, "ctx: unknown selector %s\n", selector)
		return 2
	}
	if candidate.HasCapability("observe") {
		inventory, err := resolvedMachineInventory(resolver)
		if err != nil {
			return reportError(stderr, err)
		}
		found := false
		for _, context := range inventory.Contexts {
			if context.Adapter == candidate.Manifest.Name && context.Selection != "" {
				fmt.Fprintln(stdout, context.Selection)
				found = true
			}
		}
		if found || !candidate.HasCapability("list") {
			return 0
		}
	}
	return invokeAdapter(resolver, candidate, "list", "", nil, "", stdout, stderr)
}

func listBrowsers(resolver *config.Resolver, stdout, stderr io.Writer) int {
	inventory, err := resolvedMachineInventory(resolver)
	if err != nil {
		return reportError(stderr, err)
	}
	found := false
	for _, candidate := range inventory.Contexts {
		if candidate.Runtime != "browser" || candidate.Selection == "" {
			continue
		}
		fmt.Fprintf(stdout, "%s:%s\n", candidate.Adapter, candidate.Selection)
		found = true
	}
	if !found {
		fmt.Fprintln(stderr, "ctx: no supported browser contexts found")
		return 1
	}
	return 0
}

func listManagers(resolver *config.Resolver, stdout, stderr io.Writer) int {
	inventory, err := resolvedMachineInventory(resolver)
	if err != nil {
		return reportError(stderr, err)
	}
	if printDiscoveredManagers(inventory, stdout) == 0 {
		fmt.Fprintln(stderr, "ctx: no supported manager contexts found")
		return 1
	}
	return 0
}

func listProviderFamily(resolver *config.Resolver, runtimeName string, prefix bool, stdout, stderr io.Writer) int {
	installed, err := adapterStore().List()
	if err != nil {
		return reportError(stderr, err)
	}
	found := false
	var failures bytes.Buffer
	for _, candidate := range installed {
		if !candidate.IsRuntime(runtimeName) || !candidate.HasCapability("list") {
			continue
		}
		var output, adapterError bytes.Buffer
		code := invokeAdapter(resolver, candidate, "list", "", nil, "", &output, &adapterError)
		if code != 0 {
			if adapterError.Len() > 0 {
				fmt.Fprintf(&failures, "%s: %s", candidate.Manifest.Name, adapterError.String())
				if !strings.HasSuffix(adapterError.String(), "\n") {
					failures.WriteByte('\n')
				}
			}
			continue
		}
		if output.Len() > 0 {
			found = true
			if prefix && runtimeName != "browser" {
				for _, selection := range strings.Split(strings.TrimSpace(output.String()), "\n") {
					if selection != "" {
						fmt.Fprintf(stdout, "%s:%s\n", candidate.Manifest.Name, selection)
					}
				}
			} else {
				_, _ = io.Copy(stdout, &output)
			}
		}
	}
	if !found {
		_, _ = io.Copy(stderr, &failures)
		fmt.Fprintf(stderr, "ctx: no supported %s contexts found\n", runtimeName)
		return 1
	}
	return 0
}

func openBrowser(resolver *config.Resolver, args []string, stdout, stderr io.Writer) int {
	resolved, err := resolver.Resolve("browser")
	if err != nil {
		return reportError(stderr, err)
	}
	choice := os.Getenv("CTX_BROWSER")
	if choice == "" {
		choice = resolved.Value
	}
	provider, profile, ok := strings.Cut(choice, ":")
	if !ok || provider == "" || profile == "" {
		fmt.Fprintln(stderr, "ctx: no valid browser selected; use a value from ctx ls browser")
		return 1
	}
	candidate, err := adapterStore().Load(provider)
	if err != nil || !candidate.IsRuntime("browser") || !candidate.SupportsSurface("web") {
		fmt.Fprintf(stderr, "ctx: invalid browser provider %s\n", provider)
		return 1
	}
	code := invokeAdapter(resolver, candidate, "open", profile, args, "", stdout, stderr)
	if code == 0 {
		recordWebContext(provider, profile, args, stderr)
	}
	return code
}

func doctor(resolver *config.Resolver, stdout, stderr io.Writer) int {
	failures := 0
	checked := 0
	if name, profile, err := resolver.ActiveProfile(); err != nil {
		fmt.Fprintf(stdout, "fail profile: %v\n", err)
		failures++
	} else if name != "" {
		checked++
		if profile == nil {
			fmt.Fprintf(stdout, "fail profile %s does not exist\n", name)
			failures++
		} else {
			fmt.Fprintf(stdout, "ok   profile %s\n", name)
		}
	}

	if resolved, err := resolver.Resolve("browser"); err == nil && resolved.Value != "" {
		checked++
		provider, profile, ok := strings.Cut(resolved.Value, ":")
		candidate, loadErr := adapterStore().Load(provider)
		if !ok || loadErr != nil || !candidate.IsRuntime("browser") || invokeAdapter(resolver, candidate, "doctor", profile, nil, "", io.Discard, io.Discard) != 0 {
			fmt.Fprintf(stdout, "fail browser %s is unavailable\n", resolved.Value)
			failures++
		} else {
			fmt.Fprintf(stdout, "ok   browser %s\n", resolved.Value)
		}
	}

	installed, err := adapterStore().List()
	if err != nil {
		return reportError(stderr, err)
	}
	for _, candidate := range installed {
		if candidate.IsRuntime("browser") || !candidate.SupportsSurface("shell") {
			continue
		}
		selection, err := adapterSelection(resolver, candidate)
		if err != nil || (selection == "" && candidate.IsSelectable() && !candidate.IsRuntime("manager")) {
			continue
		}
		checked++
		label := candidate.Manifest.Runtime
		if invokeAdapter(resolver, candidate, "doctor", selection, nil, "", io.Discard, io.Discard) != 0 {
			fmt.Fprintf(stdout, "fail %s %s %s is unavailable\n", label, candidate.Manifest.Name, selection)
			failures++
		} else {
			fmt.Fprintf(stdout, "ok   %s %s %s\n", label, candidate.Manifest.Name, selection)
		}
	}
	if checked == 0 {
		fmt.Fprintln(stdout, "ok   no ctx-managed selections")
	}
	if failures > 0 {
		return 1
	}
	return 0
}

func adapterSelection(resolver *config.Resolver, candidate *modpkg.Adapter) (string, error) {
	if !candidate.IsSelectable() {
		return "", nil
	}
	resolved, err := resolver.Resolve(candidate.Manifest.SelectorKey)
	if err != nil {
		return "", err
	}
	if !candidate.IsRuntime("browser") || resolved.Value == "" {
		return resolved.Value, nil
	}
	provider, profile, ok := strings.Cut(resolved.Value, ":")
	if !ok || provider != candidate.Manifest.Name {
		return "", nil
	}
	return profile, nil
}

func invokeAdapter(resolver *config.Resolver, candidate *modpkg.Adapter, operation, selection string, args []string, requestedCommand string, stdout, stderr io.Writer) int {
	return invokeAdapterIO(resolver, candidate, operation, selection, args, requestedCommand, os.Stdin, stdout, stderr)
}

func invokeAdapterIO(resolver *config.Resolver, candidate *modpkg.Adapter, operation, selection string, args []string, requestedCommand string, stdin io.Reader, stdout, stderr io.Writer) int {
	return invokeAdapterIOWithEnv(resolver, candidate, operation, selection, args, requestedCommand, stdin, stdout, stderr, nil)
}

func invokeAdapterIOWithEnv(resolver *config.Resolver, candidate *modpkg.Adapter, operation, selection string, args []string, requestedCommand string, stdin io.Reader, stdout, stderr io.Writer, extraEnv map[string]string) int {
	store := adapterStore()
	if err := store.AssertTrusted(candidate); err != nil {
		return reportError(stderr, err)
	}
	if candidate.IsRuntime("manager") && strings.HasPrefix(selection, "@") {
		registry, err := readManagerRegistry()
		if err != nil {
			return reportError(stderr, err)
		}
		instance, ok := findManagerInstance(registry, strings.TrimPrefix(selection, "@"))
		if !ok || instance.Provider != candidate.Manifest.Name {
			return reportErrorCode(stderr, fmt.Errorf("unknown %s manager instance %s", candidate.Manifest.Name, selection), 2)
		}
		selection = instance.Selection
		merged := managerInstanceEnvironment(instance)
		for key, value := range extraEnv {
			merged[key] = value
		}
		extraEnv = merged
	}
	values := map[string]string{}
	for _, key := range candidate.ConfigKeys() {
		resolved, err := resolver.Resolve(key)
		if err != nil {
			return reportError(stderr, err)
		}
		values[key] = resolved.Value
	}
	profile, _, _ := resolver.ActiveProfile()
	realCommand := ""
	if candidate.IsRuntime("manager") || (candidate.IsComputerEndpoint() && (operation == "run" || operation == "doctor" || operation == "plugin" || requestedCommand != "")) {
		commandName := ""
		if candidate.HasCommand(requestedCommand) || candidate.HasComputerCommand(requestedCommand) {
			commandName = requestedCommand
		} else if len(candidate.Manifest.Commands) > 0 {
			commandName = candidate.Manifest.Commands[0]
		} else if len(candidate.Manifest.ComputerCommands) > 0 {
			commandName = candidate.Manifest.ComputerCommands[0]
		}
		if override := extraEnv["CTX_MANAGER_COMMAND"]; override != "" {
			if err := validateManagerExecutable(override); err != nil {
				return reportErrorCode(stderr, err, 127)
			}
			realCommand = override
		} else if commandName != "" {
			var err error
			realCommand, err = launch.FindReal(commandName)
			if err != nil {
				return reportErrorCode(stderr, err, 127)
			}
		}
	}
	if pluginName := dockerInvokedPlugin(operation, args); candidate.Manifest.Name == "docker" && pluginName != "" && (extraEnv["CTX_MANAGER_PLUGIN_DIR"] != "" || extraEnv["CTX_MANAGER_PLUGIN_BUILDX"] != "" || extraEnv["CTX_MANAGER_PLUGIN_COMPOSE"] != "") {
		plugins := map[string]string{}
		for _, name := range []string{"buildx", "compose"} {
			if path := extraEnv["CTX_MANAGER_PLUGIN_"+strings.ToUpper(name)]; path != "" {
				plugins[name] = path
			}
		}
		overlay, cleanup, err := dockerConfigOverlay(extraEnv["CTX_MANAGER_PLUGIN_DIR"], plugins)
		if err != nil {
			return reportError(stderr, fmt.Errorf("prepare Docker plugins: %w", err))
		}
		defer cleanup()
		if err := validateManagerExecutable(filepath.Join(overlay, "cli-plugins", dockerPluginFilename(pluginName))); err != nil {
			return reportError(stderr, fmt.Errorf("manager Docker plugin %s: %w", pluginName, err))
		}
		copyEnv := make(map[string]string, len(extraEnv)+1)
		for key, value := range extraEnv {
			copyEnv[key] = value
		}
		copyEnv["DOCKER_CONFIG"] = overlay
		extraEnv = copyEnv
	}
	command, err := candidate.Command(modpkg.Invocation{
		Operation: operation, Selection: selection, Arguments: args, Values: values,
		Profile: profile, Project: currentDirectory(), Command: requestedCommand, RealCommand: realCommand,
	})
	if err != nil {
		return reportError(stderr, err)
	}
	if executable, err := os.Executable(); err == nil {
		command.Env = setEnvironment(command.Env, "CTX_EXECUTABLE", executable)
	}
	profileValues, err := profileEnvironment(resolver)
	if err != nil {
		return reportError(stderr, err)
	}
	for key, value := range profileValues {
		command.Env = setEnvironment(command.Env, key, value)
	}
	if candidate.IsRuntime("manager") {
		command.Env = setEnvironment(command.Env, "CTX_MANAGER_ADDRESS", "")
	}
	for key, value := range extraEnv {
		command.Env = setEnvironment(command.Env, key, value)
	}
	command.Env = stripDependencyEnvironment(command.Env)
	for space, dependency := range candidate.Manifest.Dependencies {
		command.Env = setEnvironment(command.Env, "CTX_DEPENDENCY_"+strings.ToUpper(space), dependency.Adapter)
	}
	if operation == "list" || operation == "observe" || (candidate.IsRuntime("browser") && operation == "share" && len(args) == 2 && args[0] == "status" && args[1] == "probe") {
		return runPreparedDiscovery(command, stdout, stderr)
	}
	return runPreparedIO(command, stdin, stdout, stderr)
}

func runPreparedDiscovery(command *exec.Cmd, stdout, stderr io.Writer) int {
	command.Stdin = nil
	command.Stdout = stdout
	command.Stderr = stderr
	command.WaitDelay = 2 * time.Second
	if err := command.Start(); err != nil {
		fmt.Fprintf(stderr, "ctx: %v\n", err)
		return 1
	}
	var expired atomic.Bool
	timer := time.AfterFunc(10*time.Second, func() {
		expired.Store(true)
		_ = command.Process.Kill()
	})
	err := command.Wait()
	timer.Stop()
	if expired.Load() {
		fmt.Fprintln(stderr, "ctx: adapter discovery timed out after 10 seconds")
		return 124
	}
	if err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			return exitError.ExitCode()
		}
		fmt.Fprintf(stderr, "ctx: %v\n", err)
		return 1
	}
	return 0
}

func runPrepared(command *exec.Cmd, stdout, stderr io.Writer) int {
	return runPreparedIO(command, os.Stdin, stdout, stderr)
}

func runPreparedIO(command *exec.Cmd, stdin io.Reader, stdout, stderr io.Writer) int {
	command.Stdin = stdin
	command.Stdout = stdout
	command.Stderr = stderr
	if err := command.Run(); err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			return exitError.ExitCode()
		}
		fmt.Fprintf(stderr, "ctx: %v\n", err)
		return 1
	}
	return 0
}

func currentDirectory() string {
	directory, _ := os.Getwd()
	return directory
}

func reportError(stderr io.Writer, err error) int { return reportErrorCode(stderr, err, 1) }

func reportErrorCode(stderr io.Writer, err error, code int) int {
	fmt.Fprintf(stderr, "ctx: %v\n", err)
	return code
}
