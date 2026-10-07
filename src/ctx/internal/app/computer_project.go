package app

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"

	"github.com/webong/ext/ctx/internal/config"
	modpkg "github.com/webong/ext/ctx/internal/mod"
)

func computerCommand(args []string, stdout, stderr io.Writer) int {
	if len(args) < 3 || args[0] != "hooks" {
		fmt.Fprintln(stderr, "ctx: computer requires hooks <print|install|remove> <adapter> [options]")
		return 2
	}
	return computerHooksCommand(args[1:], stdout, stderr)
}

func computerHooksCommand(args []string, stdout, stderr io.Writer) int {
	if len(args) < 2 {
		fmt.Fprintln(stderr, "ctx: computer hooks requires <print|install|remove> <adapter>")
		return 2
	}
	action, adapterName := args[0], args[1]
	if action != "print" && action != "install" && action != "remove" {
		fmt.Fprintf(stderr, "ctx: unknown computer hooks action %s\n", action)
		return 2
	}
	flags := flag.NewFlagSet("computer hooks "+action, flag.ContinueOnError)
	flags.SetOutput(stderr)
	eventsFlag := flags.String("events", "", "comma-separated native hook events")
	handlerFlag := flags.String("handler", "", "project hook handler executable")
	if err := flags.Parse(args[2:]); err != nil || len(flags.Args()) != 0 {
		return 2
	}
	candidate, err := adapterStore().Load(adapterName)
	if err != nil || !candidate.IsComputerEndpoint() || !candidate.HasComputerCapability("hook") {
		fmt.Fprintf(stderr, "ctx: %s is not an installed computer hook adapter\n", adapterName)
		return 2
	}
	if err := adapterStore().AssertTrusted(candidate); err != nil {
		return reportError(stderr, err)
	}
	if candidate.Manifest.ComputerHookSettings == "" || candidate.Manifest.ComputerHookTemplate == "" || len(candidate.Manifest.ComputerHookEvents) == 0 {
		fmt.Fprintf(stderr, "ctx: adapter %s does not declare project hook setup metadata\n", candidate.Manifest.Name)
		return 2
	}
	var events []string
	projectFile := filepath.Join(currentDirectory(), ".ctx")
	localConfig, err := config.ReadFlat(projectFile)
	if err != nil {
		return reportError(stderr, err)
	}
	storedEvents := localConfig["computer_"+candidate.Manifest.Name+"_hooks"]
	storedPatch := localConfig["computer_"+candidate.Manifest.Name+"_hooks_patch"]
	var eventsPatch map[string]any
	if action == "remove" {
		if storedPatch != "" {
			if err := json.Unmarshal([]byte(storedPatch), &eventsPatch); err != nil {
				return reportError(stderr, fmt.Errorf("stored hook patch is invalid: %w", err))
			}
		} else {
			selection := storedEvents
			if selection == "" {
				selection = strings.Join(candidate.Manifest.ComputerHookDefaults, ",")
			}
			events, err = parseComputerHookEvents(candidate, selection)
			if err != nil {
				return reportError(stderr, err)
			}
			eventsPatch, err = renderComputerHookPatch(candidate, events)
			if err != nil {
				return reportError(stderr, err)
			}
		}
	} else {
		eventSelection := *eventsFlag
		explicitEvents := false
		flags.Visit(func(value *flag.Flag) {
			if value.Name == "events" {
				explicitEvents = true
			}
		})
		if !explicitEvents {
			eventSelection = storedEvents
			if eventSelection == "" {
				eventSelection = strings.Join(candidate.Manifest.ComputerHookDefaults, ",")
			}
		}
		events, err = parseComputerHookEvents(candidate, eventSelection)
		if err != nil {
			fmt.Fprintln(stderr, "ctx:", err)
			return 2
		}
		eventsPatch, err = renderComputerHookPatch(candidate, events)
		if err != nil {
			return reportError(stderr, err)
		}
	}
	root := currentDirectory()
	settingsPath := filepath.Join(root, filepath.FromSlash(candidate.Manifest.ComputerHookSettings))
	if action == "print" {
		encoded, err := json.MarshalIndent(eventsPatch, "", "  ")
		if err != nil {
			return reportError(stderr, err)
		}
		fmt.Fprintln(stdout, string(encoded))
		return 0
	}
	if action == "install" {
		handler := *handlerFlag
		if handler == "" {
			resolved, resolveErr := newResolver()
			if resolveErr != nil {
				return reportError(stderr, resolveErr)
			}
			value, valueErr := resolved.Resolve("computer_hook_command")
			if valueErr != nil {
				return reportError(stderr, valueErr)
			}
			handler = value.Value
		}
		if handler == "" {
			handler = os.Getenv("CTX_COMPUTER_HOOK_COMMAND")
		}
		if handler == "" {
			fmt.Fprintln(stderr, "ctx: hooks install needs --handler <executable> or CTX_COMPUTER_HOOK_COMMAND")
			return 2
		}
		handler, err = resolveComputerHookHandler(root, handler)
		if err != nil {
			return reportErrorCode(stderr, err, 2)
		}
		if storedPatch != "" {
			var previous map[string]any
			if err := json.Unmarshal([]byte(storedPatch), &previous); err != nil {
				return reportError(stderr, fmt.Errorf("stored hook patch is invalid: %w", err))
			}
			if err := updateComputerHookSettings(settingsPath, previous, true); err != nil {
				return reportError(stderr, err)
			}
		}
		if err := updateComputerHookSettings(settingsPath, eventsPatch, false); err != nil {
			return reportError(stderr, err)
		}
		encodedPatch, err := json.Marshal(eventsPatch)
		if err != nil {
			return reportError(stderr, err)
		}
		if err := config.SetFlatValues(projectFile, map[string]string{
			"computer_hook_command":                                handler,
			"computer_" + candidate.Manifest.Name + "_hooks":       strings.Join(events, ","),
			"computer_" + candidate.Manifest.Name + "_hooks_patch": string(encodedPatch),
		}); err != nil {
			return reportError(stderr, err)
		}
		ensureGitExclude(projectFile)
		fmt.Fprintf(stdout, "installed %s hooks (%s) for %s\n", candidate.Manifest.Name, strings.Join(events, ","), root)
		fmt.Fprintf(stdout, "settings: %s\n", settingsPath)
		return 0
	}
	if err := updateComputerHookSettings(settingsPath, eventsPatch, true); err != nil {
		return reportError(stderr, err)
	}
	if err := config.RemoveFlat(projectFile, "computer_"+candidate.Manifest.Name+"_hooks", "computer_"+candidate.Manifest.Name+"_hooks_patch"); err != nil {
		return reportError(stderr, err)
	}
	fmt.Fprintf(stdout, "removed %s hooks from %s\n", candidate.Manifest.Name, settingsPath)
	return 0
}

func resolveComputerHookHandler(projectRoot, handler string) (string, error) {
	containsPath := filepath.IsAbs(handler) || strings.ContainsAny(handler, `/\\`)
	if containsPath {
		if !filepath.IsAbs(handler) {
			handler = filepath.Join(projectRoot, handler)
		}
	} else {
		resolved, err := exec.LookPath(handler)
		if err != nil {
			return "", fmt.Errorf("hook handler %q was not found on PATH", handler)
		}
		handler = resolved
	}
	absolute, err := filepath.Abs(handler)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return "", fmt.Errorf("hook handler %s: %w", absolute, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("hook handler %s is not a regular file", absolute)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
		return "", fmt.Errorf("hook handler %s is not executable", absolute)
	}
	return absolute, nil
}

func parseComputerHookEvents(candidate *modpkg.Adapter, value string) ([]string, error) {
	supported := make(map[string]bool, len(candidate.Manifest.ComputerHookEvents))
	for _, event := range candidate.Manifest.ComputerHookEvents {
		supported[event] = true
	}
	seen := map[string]bool{}
	events := []string{}
	for _, event := range strings.Split(value, ",") {
		event = strings.TrimSpace(event)
		if event == "" || !supported[event] {
			return nil, fmt.Errorf("unsupported hook event %q for adapter %s", event, candidate.Manifest.Name)
		}
		if !seen[event] {
			seen[event] = true
			events = append(events, event)
		}
	}
	if len(events) == 0 {
		return nil, errors.New("at least one hook event is required")
	}
	sort.Strings(events)
	return events, nil
}

func renderComputerHookPatch(candidate *modpkg.Adapter, events []string) (map[string]any, error) {
	path := filepath.Join(candidate.Directory, filepath.FromSlash(candidate.Manifest.ComputerHookTemplate))
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read adapter hook template: %w", err)
	}
	patch := map[string]any{}
	for _, event := range events {
		rendered := strings.NewReplacer("{{event}}", event, "{{adapter}}", candidate.Manifest.Name).Replace(string(contents))
		var eventPatch map[string]any
		if err := json.Unmarshal([]byte(rendered), &eventPatch); err != nil {
			return nil, fmt.Errorf("render adapter hook template: %w", err)
		}
		merged, err := mergeJSON(patch, eventPatch)
		if err != nil {
			return nil, err
		}
		patch = merged.(map[string]any)
	}
	return patch, nil
}

func updateComputerHookSettings(path string, patch map[string]any, remove bool) error {
	root := map[string]any{}
	contents, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if remove && errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if len(contents) > 0 {
		if err := json.Unmarshal(contents, &root); err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
	}
	var updated any = root
	if remove {
		updated = subtractJSON(root, patch)
	} else {
		updated, err = mergeJSON(root, patch)
		if err != nil {
			return fmt.Errorf("merge %s: %w", path, err)
		}
	}
	root, ok := updated.(map[string]any)
	if !ok {
		return fmt.Errorf("parse %s: root must be a JSON object", path)
	}
	encoded, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return err
	}
	return writeComputerSettings(path, append(encoded, '\n'))
}

func mergeJSON(current, patch any) (any, error) {
	if patchObject, ok := patch.(map[string]any); ok {
		currentObject, ok := current.(map[string]any)
		if !ok {
			if current != nil {
				return nil, errors.New("object conflicts with an existing non-object value")
			}
			currentObject = map[string]any{}
		}
		merged := make(map[string]any, len(currentObject)+len(patchObject))
		for key, value := range currentObject {
			merged[key] = value
		}
		for key, value := range patchObject {
			if old, exists := merged[key]; exists {
				result, err := mergeJSON(old, value)
				if err != nil {
					return nil, fmt.Errorf("%s: %w", key, err)
				}
				merged[key] = result
			} else {
				merged[key] = value
			}
		}
		return merged, nil
	}
	if patchArray, ok := patch.([]any); ok {
		currentArray, ok := current.([]any)
		if !ok {
			if current != nil {
				return nil, errors.New("array conflicts with an existing non-array value")
			}
			currentArray = nil
		}
		merged := append([]any(nil), currentArray...)
		for _, value := range patchArray {
			if !containsJSONValue(merged, value) {
				merged = append(merged, value)
			}
		}
		return merged, nil
	}
	if current != nil && !reflect.DeepEqual(current, patch) {
		return nil, errors.New("generated value conflicts with an existing value")
	}
	return patch, nil
}

func subtractJSON(current, patch any) any {
	if patchObject, ok := patch.(map[string]any); ok {
		currentObject, ok := current.(map[string]any)
		if !ok {
			return current
		}
		result := make(map[string]any, len(currentObject))
		for key, value := range currentObject {
			if removal, exists := patchObject[key]; exists {
				remaining := subtractJSON(value, removal)
				if remaining != nil {
					result[key] = remaining
				}
			} else {
				result[key] = value
			}
		}
		return result
	}
	if patchArray, ok := patch.([]any); ok {
		currentArray, ok := current.([]any)
		if !ok {
			return current
		}
		result := make([]any, 0, len(currentArray))
		for _, value := range currentArray {
			if !containsJSONValue(patchArray, value) {
				result = append(result, value)
			}
		}
		if len(result) == 0 {
			return nil
		}
		return result
	}
	if reflect.DeepEqual(current, patch) {
		return nil
	}
	return current
}

func containsJSONValue(values []any, wanted any) bool {
	for _, value := range values {
		if reflect.DeepEqual(value, wanted) {
			return true
		}
	}
	return false
}

func writeComputerSettings(path string, contents []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	mode := os.FileMode(0o600)
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to replace symlink %s", path)
		}
		mode = info.Mode().Perm()
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".ctx-hooks-*")
	if err != nil {
		return err
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	if err := file.Chmod(mode); err != nil {
		file.Close()
		return err
	}
	if _, err := file.Write(contents); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}
