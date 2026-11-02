package app

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/webong/ext/ctx/internal/config"
	"github.com/webong/ext/ctx/internal/launch"
	modpkg "github.com/webong/ext/pkg/ext-go/engine/host/adapter"
	"github.com/webong/ext/pkg/ext-go/engine/host/adapter/adaptercmd"
)

func adapterStore() *modpkg.Store {
	return modpkg.NewStore(modpkg.Home())
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
	return (adaptercmd.Options{Name: "ctx", Doctor: func(candidate *modpkg.Adapter, stdout, stderr io.Writer) int {
		selection, err := adapterSelection(resolver, candidate)
		if err != nil {
			return reportError(stderr, err)
		}
		return invokeAdapter(resolver, candidate, "doctor", selection, nil, "", stdout, stderr)
	}}).Run(args, stdout, stderr)
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
		if candidate.IsRuntime("browser") || !candidate.SupportsSurface("shell") || !candidate.IsKnownRuntime() {
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
		} else if commandName != "" && !(candidate.Manifest.SelfContained && candidate.IsRuntime("computer")) {
			// A self-contained computer adapter is the implementation, so there is
			// no native executable to locate.
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
	if programs := adapterPrograms(resolver, candidate); programs != nil {
		command.Env = setEnvironment(command.Env, programsEnvironment, programsEnvironmentValue(programs))
	}
	for key, value := range extraEnv {
		command.Env = setEnvironment(command.Env, key, value)
	}
	command.Env = stripDependencyEnvironment(command.Env)
	for space, dependency := range candidate.Manifest.Dependencies {
		command.Env = setEnvironment(command.Env, "CTX_DEPENDENCY_"+strings.ToUpper(space), dependency.Adapter)
	}
	if operation == "list" || operation == "observe" || operation == "programs" || (candidate.IsRuntime("browser") && operation == "share" && len(args) == 2 && args[0] == "status" && args[1] == "probe") {
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
