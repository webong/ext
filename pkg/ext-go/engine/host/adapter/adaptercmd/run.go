// Package adaptercmd provides the shared ext adapter management command.
// It owns no product selection policy; hosts may supply their doctor invocation.
package adaptercmd

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	modpkg "github.com/webong/ext/pkg/ext-go/engine/host/adapter"
)

// Options configures a management entry point. The zero value runs as ext.
type Options struct {
	// Name labels diagnostics and follow-up commands (for example ext or ctx).
	Name string
	// Doctor optionally invokes diagnostics with the consumer's selection policy.
	// Run checks package trust before calling it.
	Doctor func(*modpkg.Adapter, io.Writer, io.Writer) int
}

// Run executes management with the default ext presentation.
func Run(args []string, stdout, stderr io.Writer) int {
	return (Options{}).Run(args, stdout, stderr)
}

// Run executes adapter management with product-specific presentation and optional doctor selection.
func (o Options) Run(args []string, stdout, stderr io.Writer) int {
	if o.Name == "" {
		o.Name = "ext"
	}
	if len(args) == 0 {
		fmt.Fprintln(stderr, o.Name+": adapter requires ls, available, add, refresh, inspect, build, pack, index, install, trust, verify, test, doctor, or remove")
		return 2
	}
	store := adapterStore()
	switch args[0] {
	case "ls", "list":
		if len(args) > 2 {
			fmt.Fprintln(stderr, o.Name+": adapter ls accepts an optional runtime or surface")
			return 2
		}
		filter := ""
		if len(args) == 2 {
			filter = args[1]
			if filter != "computer" && filter != "manager" && filter != "browser" && filter != "shell" && filter != "web" {
				fmt.Fprintf(stderr, o.Name+": unknown runtime or surface %s\n", args[1])
				return 2
			}
		}
		installed, err := store.List()
		if err != nil {
			return o.reportError(stderr, err)
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
			fmt.Fprintln(stderr, o.Name+": adapter inspect needs a name")
			return 2
		}
		candidate, err := store.Load(args[1])
		if err != nil {
			return o.reportError(stderr, err)
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
		if len(candidate.Manifest.Engine) > 0 {
			fmt.Fprintf(stdout, "engine:       %s\n", strings.Join(candidate.Manifest.Engine, ","))
		}
		if len(candidate.Manifest.Role) > 0 {
			fmt.Fprintf(stdout, "role:         %s\n", strings.Join(candidate.Manifest.Role, ","))
		}
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
			fmt.Fprintln(stderr, o.Name+": adapter available takes no arguments")
			return 2
		}
		available, err := CatalogStore().List()
		if err != nil {
			return o.reportError(stderr, err)
		}
		installed := InstalledAdapterNames()
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
			fmt.Fprintln(stderr, o.Name+": adapter add needs one or more catalog names")
			return 2
		}
		for _, name := range args[1:] {
			if _, err := AddCatalogAdapter(name); err != nil {
				return o.reportError(stderr, err)
			}
			fmt.Fprintf(stdout, "installed adapter %s\n", name)
		}
		return 0
	case "refresh":
		if len(args) != 1 {
			fmt.Fprintln(stderr, o.Name+": adapter refresh takes no arguments")
			return 2
		}
		if err := RefreshCatalogAdapters(stdout); err != nil {
			return o.reportError(stderr, err)
		}
		return 0
	case "install":
		if len(args) != 2 && (len(args) != 4 || args[2] != "--sha256") {
			fmt.Fprintln(stderr, o.Name+": adapter install needs a directory or archive, optionally followed by --sha256 <digest>")
			return 2
		}
		digest := ""
		if len(args) == 4 {
			digest = args[3]
		}
		installed, err := store.InstallSource(args[1], digest)
		if err != nil {
			return o.reportError(stderr, err)
		}
		fmt.Fprintf(stdout, "installed adapter %s in %s (untrusted)\n", installed.Manifest.Name, installed.Directory)
		fmt.Fprintf(stdout, "review it, then run: %s adapter trust %s\n", o.Name, installed.Manifest.Name)
		return 0
	case "build", "pack":
		if len(args) < 2 {
			fmt.Fprintf(stderr, o.Name+": adapter %s needs a source directory\n", args[0])
			return 2
		}
		output := ""
		goos, goarch := runtime.GOOS, runtime.GOARCH
		for index := 2; index < len(args); index += 2 {
			if index+1 >= len(args) {
				fmt.Fprintln(stderr, o.Name+": adapter build/pack option needs a value")
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
				fmt.Fprintf(stderr, o.Name+": unknown adapter build/pack option %s\n", args[index])
				return 2
			}
		}
		if args[0] == "build" {
			built, err := modpkg.BuildGoPackage(modpkg.GoBuildOptions{Source: args[1], Output: output, OS: goos, Arch: goarch})
			if err != nil {
				return o.reportError(stderr, err)
			}
			fmt.Fprintf(stdout, "built adapter package %s\n", built)
			return 0
		}
		candidate, err := modpkg.LoadDirectoryForOS(args[1], goos)
		if err != nil {
			return o.reportError(stderr, err)
		}
		if output == "" {
			output = filepath.Join(filepath.Dir(candidate.Directory), fmt.Sprintf("%s-%s-%s.ctxadapter", candidate.Manifest.Name, goos, goarch))
		}
		if err := modpkg.PackageDirectory(args[1], output, goos, goarch); err != nil {
			return o.reportError(stderr, err)
		}
		fmt.Fprintf(stdout, "packed adapter %s in %s\n", candidate.Manifest.Name, output)
		return 0
	case "index":
		if len(args) < 3 {
			fmt.Fprintln(stderr, o.Name+": adapter index needs an output path and one or more archives")
			return 2
		}
		if err := modpkg.WriteIndex(args[1], args[2:]); err != nil {
			return o.reportError(stderr, err)
		}
		digest, err := modpkg.FileSHA256(args[1])
		if err != nil {
			return o.reportError(stderr, err)
		}
		fmt.Fprintf(stdout, "wrote adapter index %s\nsha256: %s\n", args[1], digest)
		return 0
	case "trust":
		if len(args) != 2 {
			fmt.Fprintln(stderr, o.Name+": adapter trust needs a name")
			return 2
		}
		candidate, err := store.Load(args[1])
		if err != nil {
			return o.reportError(stderr, err)
		}
		if err := checkShimConflicts(candidate); err != nil {
			return o.reportError(stderr, err)
		}
		if err := store.Trust(candidate); err != nil {
			return o.reportError(stderr, err)
		}
		if err := installAdapterShims(candidate); err != nil {
			return o.reportError(stderr, err)
		}
		fmt.Fprintf(stdout, "trusted adapter %s\n", candidate.Manifest.Name)
		return 0
	case "verify":
		// Read-only: loads each adapter and compares it with its recorded trust
		// digest. It never runs an adapter, changes trust, or creates the store.
		checks, err := store.Verify(args[1:]...)
		if err != nil {
			return o.reportError(stderr, err)
		}
		failed := false
		for _, check := range checks {
			switch {
			case check.Err != nil:
				failed = true
				fmt.Fprintf(stdout, "%-16s %-10s %v\n", check.Name, "invalid", check.Err)
			case check.State == modpkg.TrustTrusted:
				fmt.Fprintf(stdout, "%-16s %s\n", check.Name, check.State)
			default:
				failed = true
				fmt.Fprintf(stdout, "%-16s %-10s run %s adapter trust %s after reviewing it\n", check.Name, check.State, o.Name, check.Name)
			}
		}
		if failed {
			return 1
		}
		return 0
	case "test":
		if len(args) != 2 {
			fmt.Fprintln(stderr, o.Name+": adapter test needs an adapter directory")
			return 2
		}
		candidate, err := modpkg.LoadDirectory(args[1])
		if err != nil {
			return o.reportError(stderr, err)
		}
		invocation, err := candidate.Command(modpkg.Invocation{Operation: "doctor", Project: currentDirectory()})
		if err != nil {
			return o.reportError(stderr, err)
		}
		if code := o.runPrepared(invocation, stdout, stderr); code != 0 {
			return code
		}
		fmt.Fprintf(stdout, "adapter %s satisfies ext adapter API v%s\n", candidate.Manifest.Name, candidate.Manifest.APIVersion)
		return 0
	case "doctor":
		if len(args) != 2 {
			fmt.Fprintln(stderr, o.Name+": adapter doctor needs a name")
			return 2
		}
		candidate, err := store.Load(args[1])
		if err != nil {
			return o.reportError(stderr, err)
		}
		if err := store.AssertTrusted(candidate); err != nil {
			return o.reportError(stderr, err)
		}
		if o.Doctor != nil {
			return o.Doctor(candidate, stdout, stderr)
		}
		invocation, err := candidate.Command(modpkg.Invocation{Operation: "doctor", Project: currentDirectory()})
		if err != nil {
			return o.reportError(stderr, err)
		}
		return o.runPrepared(invocation, stdout, stderr)
	case "remove":
		force := len(args) == 3 && args[2] == "--force"
		if len(args) != 2 && !force {
			fmt.Fprintln(stderr, o.Name+": adapter remove needs a name, optionally followed by --force")
			return 2
		}
		candidate, err := store.Load(args[1])
		if err != nil {
			return o.reportError(stderr, err)
		}
		if !force {
			installed, err := store.List()
			if err != nil {
				return o.reportError(stderr, err)
			}
			for _, dependent := range installed {
				for _, dependency := range dependent.Manifest.Dependencies {
					if dependency.Adapter == candidate.Manifest.Name {
						return o.reportError(stderr, fmt.Errorf("adapter %s is required by %s; remove the dependent first or pass --force", candidate.Manifest.Name, dependent.Manifest.Name))
					}
				}
			}
		}
		if err := removeAdapterShims(candidate); err != nil {
			return o.reportError(stderr, err)
		}
		if err := store.Remove(args[1]); err != nil {
			return o.reportError(stderr, err)
		}
		fmt.Fprintf(stdout, "removed adapter %s\n", args[1])
		return 0
	default:
		fmt.Fprintln(stderr, o.Name+": adapter requires ls, available, add, refresh, inspect, build, pack, index, install, trust, verify, test, doctor, or remove")
		return 2
	}
}

func (o Options) runPrepared(command *exec.Cmd, stdout, stderr io.Writer) int {
	return o.runPreparedIO(command, os.Stdin, stdout, stderr)
}

func (o Options) runPreparedIO(command *exec.Cmd, stdin io.Reader, stdout, stderr io.Writer) int {
	command.Stdin = stdin
	command.Stdout = stdout
	command.Stderr = stderr
	if err := command.Run(); err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			return exitError.ExitCode()
		}
		fmt.Fprintf(stderr, "%s: %v\n", o.Name, err)
		return 1
	}
	return 0
}

func currentDirectory() string {
	directory, _ := os.Getwd()
	return directory
}

func (o Options) reportError(stderr io.Writer, err error) int {
	fmt.Fprintf(stderr, "%s: %v\n", o.Name, err)
	return 1
}
