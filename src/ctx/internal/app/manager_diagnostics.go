package app

import (
	"fmt"
	"io"
	"strings"

	"github.com/webong/ext/src/ctx/internal/config"
	modpkg "github.com/webong/ext/src/ctx/internal/mod"
)

func managerAppAdapter(candidate *modpkg.Adapter) bool {
	if !candidate.IsRuntime("manager") || !candidate.Manifest.SelfContained || !candidate.HasCapability("run") || !candidate.HasCapability("doctor") {
		return false
	}
	for _, kind := range candidate.Manifest.Supports {
		if kind == "virtualizer" {
			return true
		}
	}
	return false
}

func managerAppCommand(resolver *config.Resolver, name, action string, stdout, stderr io.Writer) int {
	candidate, err := adapterStore().Load(name)
	if err != nil || !managerAppAdapter(candidate) {
		return reportErrorCode(stderr, fmt.Errorf("unknown manager app adapter %s", name), 2)
	}
	if action == "doctor" {
		return invokeAdapter(resolver, candidate, "doctor", "", nil, "", stdout, stderr)
	}
	if action != "status" && action != "start" && action != "stop" {
		return reportErrorCode(stderr, fmt.Errorf("manager app action must be status, start, stop, or doctor"), 2)
	}
	return invokeAdapter(resolver, candidate, "run", "", []string{action}, "", stdout, stderr)
}

func doctorManagers(resolver *config.Resolver, registry managerRegistry, name string, stdout, stderr io.Writer) int {
	failures := 0
	checked := 0
	appName := ""
	for _, instance := range registry.Instances {
		if name != "" && instance.Name != name {
			continue
		}
		checked++
		if name != "" {
			appName = strings.ReplaceAll(instance.Virtualizer, "-", "_")
		}
		if err := validateManagerToolchain(instance); err != nil {
			fmt.Fprintf(stdout, "fail @%s toolchain: %v\n", instance.Name, err)
			failures++
		} else if instance.Command != "" || instance.PluginDir != "" || len(instance.Plugins) != 0 {
			fmt.Fprintf(stdout, "ok   @%s toolchain configured\n", instance.Name)
		} else {
			fmt.Fprintf(stdout, "ok   @%s uses the PATH command and global plugins\n", instance.Name)
		}
	}
	installed, err := adapterStore().List()
	if err != nil {
		return reportError(stderr, err)
	}
	for _, candidate := range installed {
		if !managerAppAdapter(candidate) || (name != "" && candidate.Manifest.Name != name && candidate.Manifest.Name != appName) {
			continue
		}
		checked++
		fmt.Fprintf(stdout, "%s:\n", candidate.Manifest.Name)
		if code := invokeAdapter(resolver, candidate, "doctor", "", nil, "", stdout, stderr); code != 0 {
			failures++
		}
	}
	if checked == 0 {
		fmt.Fprintln(stdout, "ok   no registered managers or installed manager app adapters")
	}
	if failures != 0 {
		return 1
	}
	return 0
}
