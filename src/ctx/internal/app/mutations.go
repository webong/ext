package app

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/webong/ctx/src/ctx/internal/config"
	modpkg "github.com/webong/ctx/src/ctx/internal/mod"
)

var profileNamePattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
var environmentNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func setContext(resolver *config.Resolver, args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		provider, selection, qualified := strings.Cut(args[0], ":")
		if qualified {
			candidate, err := adapterStore().Load(provider)
			if provider == "" || selection == "" || err != nil || !candidate.IsSelectable() {
				fmt.Fprintf(stderr, "ctx: invalid or unavailable adapter selection %s\n", args[0])
				return 2
			}
			if candidate.IsRuntime("browser") {
				args = append([]string{"browser", args[0]}, args[1:]...)
			} else {
				args = append([]string{provider, selection}, args[1:]...)
			}
		}
	}
	if len(args) < 2 {
		fmt.Fprintln(stderr, "ctx: set needs an adapter:selection or a selector and name")
		return 2
	}
	selector, selection, options := args[0], args[1], args[2:]
	if selection == "" || strings.ContainsAny(selection, "\r\n") {
		fmt.Fprintln(stderr, "ctx: invalid empty or multiline selection")
		return 2
	}
	projectFile := filepath.Join(currentDirectory(), ".ctx")
	switch selector {
	case "browser":
		if len(options) != 0 {
			fmt.Fprintln(stderr, "ctx: browser set does not accept options")
			return 2
		}
		provider, profile, ok := strings.Cut(selection, ":")
		candidate, err := adapterStore().Load(provider)
		if !ok || profile == "" || err != nil || !candidate.IsRuntime("browser") || invokeAdapter(resolver, candidate, "validate", profile, nil, "", io.Discard, stderr) != 0 {
			fmt.Fprintf(stderr, "ctx: browser selection %s is unavailable\n", selection)
			return 1
		}
		if err := config.SetFlat(projectFile, "browser", selection); err != nil {
			return reportError(stderr, err)
		}
	default:
		candidate, err := adapterStore().Load(selector)
		if err != nil || !candidate.IsSelectable() || candidate.IsRuntime("browser") {
			fmt.Fprintf(stderr, "ctx: unknown selector %s\n", selector)
			return 2
		}
		if candidate.IsRuntime("manager") {
			global := len(options) == 1 && options[0] == "--global"
			if len(options) != 0 && !global {
				fmt.Fprintln(stderr, "ctx: manager provider set only accepts --global")
				return 2
			}
			if code := invokeAdapter(resolver, candidate, "validate", selection, nil, "", io.Discard, stderr); code != 0 {
				return code
			}
			if global {
				if err := config.SetRoot(resolver.ConfigPath, candidate.Manifest.SelectorKey+"_default", selection); err != nil {
					return reportError(stderr, err)
				}
				fmt.Fprintf(stdout, "%s fallback: %s\n", selector, selection)
				return 0
			}
			if err := config.SetFlat(projectFile, candidate.Manifest.SelectorKey, selection); err != nil {
				return reportError(stderr, err)
			}
		} else {
			values, code := configureAdapterSelection(resolver, candidate, selection, options, stderr)
			if code != 0 {
				return code
			}
			if err := config.SetFlatValues(projectFile, values); err != nil {
				return reportError(stderr, err)
			}
		}
	}
	ensureGitExclude(projectFile)
	fmt.Fprintf(stdout, "%s: %s in %s\n", selector, selection, projectFile)
	return 0
}

func configureAdapterSelection(resolver *config.Resolver, candidate *modpkg.Adapter, selection string, options []string, stderr io.Writer) (map[string]string, int) {
	if !candidate.HasCapability("configure") {
		if len(options) != 0 {
			fmt.Fprintf(stderr, "ctx: adapter %s does not accept selection options\n", candidate.Manifest.Name)
			return nil, 2
		}
		if code := invokeAdapter(resolver, candidate, "validate", selection, nil, "", io.Discard, stderr); code != 0 {
			return nil, code
		}
		return map[string]string{candidate.Manifest.SelectorKey: selection}, 0
	}
	var output bytes.Buffer
	if code := invokeAdapter(resolver, candidate, "configure", selection, options, candidate.Manifest.Name, &output, stderr); code != 0 {
		return nil, code
	}
	values, err := parseAdapterConfigurationValues(candidate, output.String())
	if err != nil {
		return nil, reportError(stderr, err)
	}
	return values, 0
}

func parseAdapterConfigurationValues(candidate *modpkg.Adapter, output string) (map[string]string, error) {
	allowed := map[string]bool{}
	for _, key := range candidate.ConfigKeys() {
		allowed[key] = true
	}
	values := map[string]string{}
	for _, line := range strings.Split(output, "\n") {
		// PowerShell emits CRLF records. Strip the line-ending CR while
		// continuing to reject carriage returns embedded in a value.
		line = strings.TrimSuffix(line, "\r")
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 || !allowed[parts[0]] || parts[1] == "" || strings.ContainsAny(parts[1], "\r\n") {
			return nil, fmt.Errorf("adapter %s returned an invalid configuration record", candidate.Manifest.Name)
		}
		values[parts[0]] = parts[1]
	}
	if values[candidate.Manifest.SelectorKey] == "" {
		return nil, fmt.Errorf("adapter %s did not return selector key %s", candidate.Manifest.Name, candidate.Manifest.SelectorKey)
	}
	return values, nil
}

func clearContext(args []string, stdout, stderr io.Writer) int {
	if len(args) > 1 {
		fmt.Fprintln(stderr, "ctx: clear accepts at most one selector")
		return 2
	}
	projectFile := filepath.Join(currentDirectory(), ".ctx")
	if len(args) == 0 {
		if err := os.Remove(projectFile); err != nil && !os.IsNotExist(err) {
			return reportError(stderr, err)
		}
		fmt.Fprintf(stdout, "cleared %s\n", projectFile)
		return 0
	}
	selector := args[0]
	keys := []string{selector}
	if selector == "profile" || selector == "browser" {
		// The selector key is identical to its command name.
	} else if candidate, err := adapterStore().Load(selector); err == nil && candidate.IsSelectable() {
		keys = candidate.ConfigKeys()
	} else {
		fmt.Fprintf(stderr, "ctx: cannot clear %s\n", selector)
		return 2
	}
	if err := config.RemoveFlat(projectFile, keys...); err != nil {
		return reportError(stderr, err)
	}
	fmt.Fprintf(stdout, "cleared %s in %s\n", selector, projectFile)
	return 0
}

func profileCommand(resolver *config.Resolver, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "ctx: profile requires ls, show, use, set, unset, env, env-unset, or clear")
		return 2
	}
	switch args[0] {
	case "ls", "list":
		if len(args) != 1 {
			return profileUsage(stderr, "profile ls takes no arguments")
		}
		names := make([]string, 0, len(resolver.Config.Profiles))
		for name := range resolver.Config.Profiles {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			fmt.Fprintln(stdout, name)
		}
		return 0
	case "show":
		if len(args) != 2 {
			return profileUsage(stderr, "profile show needs a profile name")
		}
		if resolver.Config.Profiles[args[1]] == nil {
			fmt.Fprintf(stderr, "ctx: unknown profile %s\n", args[1])
			return 1
		}
		lines, err := config.SectionLines(resolver.ConfigPath, profileHeader(args[1]))
		if err != nil {
			return reportError(stderr, err)
		}
		for _, line := range lines {
			fmt.Fprintln(stdout, line)
		}
		environment, err := config.SectionLines(resolver.ConfigPath, profileEnvironmentHeader(args[1]))
		if err != nil {
			return reportError(stderr, err)
		}
		if len(environment) > 0 {
			fmt.Fprintln(stdout, "[env]")
			for _, line := range environment {
				fmt.Fprintln(stdout, line)
			}
		}
		return 0
	case "use":
		if len(args) != 2 || !profileNamePattern.MatchString(args[1]) {
			return profileUsage(stderr, "profile use needs a valid profile name")
		}
		if resolver.Config.Profiles[args[1]] == nil {
			fmt.Fprintf(stderr, "ctx: unknown profile %s\n", args[1])
			return 1
		}
		path := filepath.Join(currentDirectory(), ".ctx")
		if err := config.SetFlat(path, "profile", args[1]); err != nil {
			return reportError(stderr, err)
		}
		ensureGitExclude(path)
		fmt.Fprintf(stdout, "profile: %s in %s\n", args[1], path)
		return 0
	case "set":
		if len(args) != 4 || !profileNamePattern.MatchString(args[1]) || args[3] == "" {
			return profileUsage(stderr, "profile set needs name, key, and value")
		}
		if !validProfileKey(args[2]) {
			fmt.Fprintf(stderr, "ctx: unsupported profile key %s\n", args[2])
			return 2
		}
		if err := config.SetSection(resolver.ConfigPath, profileHeader(args[1]), args[2], args[3]); err != nil {
			return reportError(stderr, err)
		}
		fmt.Fprintf(stdout, "profile %s: %s = %s\n", args[1], args[2], args[3])
		return 0
	case "unset":
		if len(args) != 3 || !profileNamePattern.MatchString(args[1]) {
			return profileUsage(stderr, "profile unset needs name and key")
		}
		if err := config.RemoveSectionValue(resolver.ConfigPath, profileHeader(args[1]), args[2]); err != nil {
			return reportError(stderr, err)
		}
		fmt.Fprintf(stdout, "profile %s: cleared %s\n", args[1], args[2])
		return 0
	case "env":
		if len(args) != 4 || !profileNamePattern.MatchString(args[1]) || !environmentNamePattern.MatchString(args[2]) {
			return profileUsage(stderr, "profile env needs name, variable, and value")
		}
		if err := config.SetSection(resolver.ConfigPath, profileEnvironmentHeader(args[1]), args[2], args[3]); err != nil {
			return reportError(stderr, err)
		}
		fmt.Fprintf(stdout, "profile %s environment: %s set\n", args[1], args[2])
		return 0
	case "env-unset":
		if len(args) != 3 || !profileNamePattern.MatchString(args[1]) || !environmentNamePattern.MatchString(args[2]) {
			return profileUsage(stderr, "profile env-unset needs name and variable")
		}
		if err := config.RemoveSectionValue(resolver.ConfigPath, profileEnvironmentHeader(args[1]), args[2]); err != nil {
			return reportError(stderr, err)
		}
		fmt.Fprintf(stdout, "profile %s environment: cleared %s\n", args[1], args[2])
		return 0
	case "clear":
		if len(args) != 1 {
			return profileUsage(stderr, "profile clear takes no name")
		}
		return clearContext([]string{"profile"}, stdout, stderr)
	default:
		fmt.Fprintln(stderr, "ctx: profile requires ls, show, use, set, unset, env, env-unset, or clear")
		return 2
	}
}

func validProfileKey(key string) bool {
	switch key {
	case "browser", "shell_path":
		return true
	}
	installed, err := adapterStore().List()
	if err != nil {
		return false
	}
	for _, candidate := range installed {
		if !candidate.IsSelectable() {
			continue
		}
		for _, configKey := range candidate.ConfigKeys() {
			if key == configKey {
				return true
			}
		}
	}
	return false
}

func profileHeader(name string) string { return "[profiles.\"" + name + "\"]" }

func profileEnvironmentHeader(name string) string { return "[profiles.\"" + name + "\".env]" }

func profileUsage(stderr io.Writer, message string) int {
	fmt.Fprintf(stderr, "ctx: %s\n", message)
	return 2
}

func ensureGitExclude(projectFile string) {
	command := exec.Command("git", "rev-parse", "--git-path", "info/exclude")
	command.Dir = filepath.Dir(projectFile)
	output, err := command.Output()
	if err != nil {
		return
	}
	exclude := strings.TrimSpace(string(output))
	if !filepath.IsAbs(exclude) {
		exclude = filepath.Join(command.Dir, exclude)
	}
	contents, _ := os.ReadFile(exclude)
	for _, line := range strings.Split(strings.ReplaceAll(string(contents), "\r\n", "\n"), "\n") {
		if strings.TrimSpace(line) == ".ctx" {
			return
		}
	}
	_ = os.MkdirAll(filepath.Dir(exclude), 0o700)
	file, err := os.OpenFile(exclude, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err == nil {
		_, _ = io.WriteString(file, "\n.ctx\n")
		_ = file.Close()
	}
}
