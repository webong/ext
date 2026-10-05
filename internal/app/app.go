package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/webong/ctx/graph/system"
	"github.com/webong/ctx/internal/config"
	"github.com/webong/ctx/internal/launch"
	"github.com/webong/ctx/internal/platform"
)

var Version = "0.8.0-dev"

func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		usage(stdout)
		return 0
	}
	if args[0] == "version" || args[0] == "--version" || args[0] == "-V" {
		fmt.Fprintf(stdout, "ctx %s\n", Version)
		return 0
	}
	if args[0] == "real" {
		if len(args) != 2 || filepath.Base(args[1]) != args[1] {
			fmt.Fprintln(stderr, "ctx: real needs a command name")
			return 2
		}
		real, err := launch.FindReal(args[1])
		if err != nil {
			return reportErrorCode(stderr, err, 127)
		}
		fmt.Fprintln(stdout, real)
		return 0
	}
	if args[0] == "completion" {
		return completion(args[1:], stdout, stderr)
	}
	if args[0] == "hook" {
		if len(args) >= 2 && args[1] == "computer" {
			return computerHook(args[2:], stdout, stderr)
		}
		return shellHook(args[1:], stdout, stderr)
	}
	if args[0] == "__observe-shell" {
		resolver, err := newResolver()
		if err != nil {
			fmt.Fprintf(stderr, "ctx: %v\n", err)
			return 1
		}
		profile, _, _ := resolver.ActiveProfile()
		system, err := systemgraph.Open(configHomePath())
		if err != nil {
			fmt.Fprintf(stderr, "ctx: %v\n", err)
			return 1
		}
		defer system.Close()
		if err := system.ObserveShell(context.Background(), currentDirectory(), profile, observedSelections(resolver)); err != nil {
			fmt.Fprintf(stderr, "ctx: %v\n", err)
			return 1
		}
		return 0
	}
	if args[0] == "graph" {
		return graphCommand(args[1:], stdout, stderr)
	}
	resolver, err := newResolver()
	if err != nil {
		fmt.Fprintf(stderr, "ctx: %v\n", err)
		return 1
	}
	recordSystemContext(resolver, stderr)
	if strings.HasPrefix(args[0], "share:") {
		return shareSpaceCommand(resolver, strings.TrimPrefix(args[0], "share:"), args[1:], stdout, stderr)
	}
	switch args[0] {
	case "resolve":
		if len(args) != 2 {
			fmt.Fprintln(stderr, "ctx: resolve needs a key")
			return 2
		}
		resolved, err := resolver.Resolve(args[1])
		if err != nil {
			fmt.Fprintf(stderr, "ctx: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, resolved.Value)
		return 0
	case "status":
		return status(resolver, args[1:], stdout, stderr)
	case "explain":
		return explain(resolver, stdout, stderr)
	case "env":
		return showEnvironment(resolver, stdout, stderr)
	case "adapter":
		return adapterCommand(resolver, args[1:], stdout, stderr)
	case "computer":
		return computerCommand(args[1:], stdout, stderr)
	case "manager":
		return managerCommand(resolver, args[1:], stdout, stderr)
	case "plugin":
		if len(args) < 2 || args[1] != "computer" {
			fmt.Fprintln(stderr, "ctx: plugin requires computer <adapter> [arguments...]")
			return 2
		}
		return computerPlugin(resolver, args[2:], stdout, stderr)
	case "setup":
		return setupCommand(args[1:], os.Stdin, stdout, stderr)
	case "set":
		return setContext(resolver, args[1:], stdout, stderr)
	case "clear":
		return clearContext(args[1:], stdout, stderr)
	case "profile":
		return profileCommand(resolver, args[1:], stdout, stderr)
	case "ls":
		return listContexts(resolver, args[1:], stdout, stderr)
	case "open":
		return openBrowser(resolver, args[1:], stdout, stderr)
	case "browser":
		return browserCommand(resolver, args[1:], stdout, stderr)
	case "credential":
		return credentialCommand(resolver, args[1:], os.Stdin, stdout, stderr)
	case "doctor":
		return doctor(resolver, stdout, stderr)
	case "build":
		return buildImage(resolver, args[1:], stdout, stderr)
	case "run":
		return runCommand(resolver, args[1:], stdout, stderr)
	case "shell":
		return shell(resolver, args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "ctx: unknown command %s\n", args[0])
		return 2
	}
}

func shellHook(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "ctx: hook needs bash, zsh, or powershell")
		return 2
	}
	switch args[0] {
	case "bash":
		fmt.Fprintln(stdout, `__ctx_observe() { command ctx __observe-shell >/dev/null 2>&1; }
if [[ ";${PROMPT_COMMAND-};" != *";__ctx_observe;"* ]]; then
  PROMPT_COMMAND="${PROMPT_COMMAND:+${PROMPT_COMMAND}; }__ctx_observe"
fi`)
	case "zsh":
		fmt.Fprintln(stdout, `autoload -Uz add-zsh-hook
__ctx_observe() { command ctx __observe-shell >/dev/null 2>&1; }
add-zsh-hook precmd __ctx_observe`)
	case "powershell":
		fmt.Fprintln(stdout, `if (-not (Test-Path variable:global:__ctx_original_prompt)) {
  $global:__ctx_original_prompt = (Get-Item Function:prompt).ScriptBlock
}
function global:prompt {
  & ctx __observe-shell *> $null
  & $global:__ctx_original_prompt
}`)
	default:
		fmt.Fprintf(stderr, "ctx: unsupported shell %s\n", args[0])
		return 2
	}
	return 0
}

func usage(output io.Writer) {
	fmt.Fprintln(output, `ctx — project-local contexts for development tools
usage:
  ctx status [selector]
  ctx real <command>
  ctx completion powershell
  ctx hook <bash|zsh|powershell>
  ctx hook computer <adapter> <event> Read event JSON from stdin and invoke a computer hook
  ctx plugin computer <adapter> [arguments...]
  ctx resolve <key>
  ctx explain
  ctx env
  ctx set <adapter>:<selection> [options]
  ctx set <selector> <name> [options]
  ctx clear [selector|profile]
  ctx profile <ls|show|use|set|unset|env|env-unset|clear> [arguments...]
  ctx adapter <ls [runtime|surface]|available|add|refresh|inspect|build|pack|index|install|trust|test|doctor|remove> [arguments...]
  ctx computer hooks <print|install|remove> <adapter> [--events <name,...>] [--handler <executable>]
  ctx manager add <name> --provider <adapter> --selection <context>
      [--virtualizer <product>] [--machine <vm>] [--address <adapter-address>]
      [--command </absolute/path>] [--plugin-dir </absolute/path>]
      [--plugin buildx=</absolute/path>] [--plugin compose=</absolute/path>]
      [--offline]
  ctx manager <ls|show|doctor|remove> [name]
  ctx manager apps
  ctx manager app <adapter> <status|start|stop|doctor>
  ctx setup [adapters] [--all|--minimal|--adapters <name,...>]
  ctx ls <selector>
  ctx open [URL...]
  ctx browser manage <extension|userscript|bookmarklet|session> <action> [--target <browser:profile>] [--input <json>]
  ctx doctor
  ctx build [provider|@instance] --cache-ref <registry-ref> [--] <build arguments>
  ctx share:manager image <sync|copy> <source> <target> <image>...
  ctx share:manager volume <export|import|copy> [arguments...]
      endpoints: @instance or provider:selection
  ctx share:browser cookie list [--from <browser:profile>] --site <URL>
  ctx share:browser cookie query --site <URL> [--from <browser:profile>] [--name <cookie>]
      [--mode merge|first] (--to-file <path> | --stdout)
  ctx share:browser cookie copy [--from <browser:profile>] --site <URL> --name <cookie>
      [--domain <domain>] [--path <path>] [--id <row-id>] [--ref <reference>]
      [--attribute <namespace.key=value>]
      (--to-profile <browser:profile> | --to-file <path> | --stdout) [--replace]
  ctx share:browser cookie import (--from-file <path> | --stdin) --to-profile <browser:profile> [--replace]
  ctx share:browser policy export [--from <browser:profile>] (--to-file <path> | --stdout)
  ctx share:browser capabilities [--from <browser:profile>]
  ctx share:browser <resource> <list|export|copy|import> [bridge options] [-- adapter arguments...]
  ctx credential <get|put|copy> [arguments...]
  ctx share:credential <get|put|copy> [arguments...]
  ctx share:computer (reserved; unavailable)
  ctx share:<space> [arguments...] (when an adapter registers the space)
  ctx run <provider-or-adapter-command> [arguments...]
  ctx run -- <command> [arguments...]
  ctx shell [--shell <executable>] [-- <command> [arguments...]]
  ctx graph scan
  ctx graph shells
  ctx graph filesystems
  ctx graph webviews
  ctx graph processes [--limit 4096] [--timeout 10s]
  ctx graph process <pid> [--resource-limit 1024] [--timeout 10s]
  ctx graph resolve [runtime|all] [capability...] [--supports <kind>] [--share <resource.operation>]
  ctx graph resolve shell [--name <name>] [--select <executable>]
  ctx graph resolve filesystem [--path <path>] [--type <type>] [--writable] [--min-free <bytes>] [--select <mount>]
  ctx graph resolve webview [--engine <engine>] [--api <api>] [--abi <generation>] [--version <version>] [--arch <architecture>] [--select <location>]
  ctx graph <scan|shells|filesystems|webviews|processes|process|status|vertices|edges|snapshot|changes> [arguments...]
  ctx version`)
}

func newResolver() (*config.Resolver, error) {
	workingDir, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return config.NewResolver(workingDir, homeDir, filepath.Join(configHomePath(), "config.toml"))
}

func configHomePath() string {
	if home := os.Getenv("CTX_HOME"); home != "" {
		return home
	}
	return platform.DefaultConfigHome()
}

func status(resolver *config.Resolver, selectors []string, stdout, stderr io.Writer) int {
	showAll := len(selectors) == 0
	if showAll {
		selectors = []string{"browser", "profile"}
		if installed, err := adapterStore().List(); err == nil {
			for _, candidate := range installed {
				if candidate.IsSelectable() && !candidate.IsRuntime("browser") {
					selectors = append(selectors, candidate.Manifest.Name)
				}
			}
		}
	}
	if !showAll && len(selectors) != 1 {
		fmt.Fprintln(stderr, "ctx: status accepts at most one selector")
		return 2
	}
	var inventory systemgraph.Inventory
	if showAll || selectors[0] != "profile" {
		if refreshed, err := freshMachineInventory(resolver); err == nil {
			inventory = refreshed
		} else {
			fmt.Fprintf(stderr, "ctx: warning: could not refresh machine graph: %v\n", err)
		}
	}
	for _, selector := range selectors {
		candidate, _ := adapterStore().Load(selector)
		overrideEnv := []string(nil)
		if candidate != nil && candidate.IsSelectable() {
			overrideEnv = candidate.Manifest.OverrideEnv
		}
		if value, source := environmentOverride(selector, overrideEnv); value != "" {
			fmt.Fprintf(stdout, "%s: %s (%s)%s\n", selector, value, source, observedStatus(selector, value, inventory))
			continue
		}
		key := selector
		if candidate != nil && candidate.IsSelectable() {
			key = candidate.Manifest.SelectorKey
		}
		resolved, err := resolver.Resolve(key)
		if err != nil {
			fmt.Fprintf(stderr, "ctx: %v\n", err)
			return 1
		}
		if resolved.Value == "" {
			fmt.Fprintf(stdout, "%s: <not selected>\n", selector)
		} else {
			fmt.Fprintf(stdout, "%s: %s (%s)%s\n", selector, resolved.Value, resolved.Source, observedStatus(selector, resolved.Value, inventory))
		}
	}
	return 0
}

func observedStatus(selector, value string, inventory systemgraph.Inventory) string {
	if selector == "browser" {
		provider, profile, ok := strings.Cut(value, ":")
		if !ok {
			return ""
		}
		candidate, err := inventory.Find(provider, profile)
		if err == nil && candidate.Runtime == "browser" {
			return " [observed]"
		}
		return ""
	}
	if candidate, err := inventory.Find(selector, value); err == nil && candidate.Runtime != "browser" {
		return " [observed]"
	}
	return ""
}

func environmentOverride(selector string, providerVariables []string) (string, string) {
	checks := providerVariables
	if selector == "browser" {
		checks = append([]string{"CTX_BROWSER"}, checks...)
	}
	for _, key := range checks {
		if value := os.Getenv(key); value != "" {
			return value, key
		}
	}
	return "", ""
}

func explain(resolver *config.Resolver, stdout, stderr io.Writer) int {
	items := []struct{ label, key string }{{"profile", "profile"}, {"browser", "browser"}, {"shell-path", "shell_path"}}
	if installed, err := adapterStore().List(); err == nil {
		for _, candidate := range installed {
			if candidate.IsSelectable() && !candidate.IsRuntime("browser") {
				items = append(items, struct{ label, key string }{candidate.Manifest.Name, candidate.Manifest.SelectorKey})
			}
		}
	}
	for _, item := range items {
		resolved, err := resolver.Resolve(item.key)
		if err != nil {
			fmt.Fprintf(stderr, "ctx: %v\n", err)
			return 1
		}
		if resolved.Value == "" {
			fmt.Fprintf(stdout, "%-16s <native default>\n", item.label)
		} else {
			fmt.Fprintf(stdout, "%-16s %s (%s)\n", item.label, resolved.Value, resolved.Source)
		}
	}
	return 0
}

func profileEnvironment(resolver *config.Resolver) (map[string]string, error) {
	result := map[string]string{}
	_, profile, err := resolver.ActiveProfile()
	if err != nil {
		return result, err
	}
	if profile != nil {
		for key, value := range profile.Env {
			if os.Getenv(key) == "" {
				result[key] = value
			}
		}
	}
	resolvedPath, err := resolver.Resolve("shell_path")
	if err != nil {
		return result, err
	}
	if resolvedPath.Value != "" {
		result["PATH"] = resolvedPath.Value + string(os.PathListSeparator) + os.Getenv("PATH")
	}
	return result, nil
}

func showEnvironment(resolver *config.Resolver, stdout, stderr io.Writer) int {
	values, err := profileEnvironment(resolver)
	if err != nil {
		fmt.Fprintf(stderr, "ctx: %v\n", err)
		return 1
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		fmt.Fprintf(stdout, "%s=%s\n", key, values[key])
	}
	return 0
}

func runCommand(resolver *config.Resolver, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "ctx: run needs a tool or -- followed by a command")
		return 2
	}
	if args[0] == "--" {
		if len(args) == 1 {
			fmt.Fprintln(stderr, "ctx: run -- needs a command")
			return 2
		}
		return execute(resolver, args[1], args[2:], stdout, stderr)
	}
	return runAdapterTool(resolver, args[0], args[1:], stdout, stderr)
}

func shell(resolver *config.Resolver, args []string, stdout, stderr io.Writer) int {
	shellName := ""
	if len(args) > 0 && args[0] == "--shell" {
		if len(args) < 2 || args[1] == "" {
			fmt.Fprintln(stderr, "ctx: --shell needs an executable")
			return 2
		}
		shellName, args = args[1], args[2:]
	} else if len(args) > 0 && strings.HasPrefix(args[0], "--shell=") {
		shellName, args = strings.TrimPrefix(args[0], "--shell="), args[1:]
		if shellName == "" {
			fmt.Fprintln(stderr, "ctx: --shell needs an executable")
			return 2
		}
	}
	if len(args) > 0 && args[0] == "--" {
		if len(args) == 1 {
			fmt.Fprintln(stderr, "ctx: shell -- needs a command")
			return 2
		}
		return execute(resolver, args[1], args[2:], stdout, stderr)
	}
	if len(args) != 0 {
		fmt.Fprintln(stderr, "ctx: shell only accepts --shell followed by an executable or -- followed by a command")
		return 2
	}
	values, err := profileEnvironment(resolver)
	if err != nil {
		return reportError(stderr, err)
	}
	if shellName == "" {
		shellName = values["CTX_SHELL"]
		if shellName == "" {
			shellName = platform.DefaultShell()
		}
	}
	searchPath := os.Getenv("PATH")
	if values["PATH"] != "" {
		searchPath = values["PATH"]
	}
	prepared, err := prepareHostOperation(systemgraph.OperationRequirements{
		Operation: "shell.launch",
		Shell:     &systemgraph.ShellRequirement{Select: shellName, SearchPath: &searchPath},
	})
	if err != nil {
		return reportError(stderr, err)
	}
	return execute(resolver, prepared.Shell.Path, nil, stdout, stderr)
}

func execute(resolver *config.Resolver, name string, args []string, stdout, stderr io.Writer) int {
	command := exec.Command(name, args...)
	command.Stdin = os.Stdin
	command.Stdout = stdout
	command.Stderr = stderr
	command.Env = os.Environ()
	values, err := profileEnvironment(resolver)
	if err != nil {
		fmt.Fprintf(stderr, "ctx: %v\n", err)
		return 1
	}
	for key, value := range values {
		command.Env = setEnvironment(command.Env, key, value)
	}
	if err := command.Run(); err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			return exitError.ExitCode()
		}
		fmt.Fprintf(stderr, "ctx: %v\n", err)
		return 1
	}
	return 0
}

func setEnvironment(values []string, key, value string) []string {
	prefix := strings.ToUpper(key) + "="
	for index, entry := range values {
		if strings.HasPrefix(strings.ToUpper(entry), prefix) {
			values[index] = key + "=" + value
			return values
		}
	}
	return append(values, key+"="+value)
}
