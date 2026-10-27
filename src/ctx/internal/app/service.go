package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/webong/ext/res/system/service"
	"github.com/webong/ext/res/system/service/launchd"
	"github.com/webong/ext/res/system/service/taskscheduler"
)

func serviceCommand(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "ctx: service needs one of render, status, register, unregister, start, or stop")
		return 2
	}
	action := args[0]
	switch action {
	case "render", "status", "register", "unregister", "start", "stop":
	default:
		fmt.Fprintf(stderr, "ctx: unknown service action %s\n", action)
		return 2
	}
	options, err := parseServiceOptions(args[1:])
	if err != nil {
		fmt.Fprintf(stderr, "ctx: %v\n", err)
		return 2
	}
	platform := service.Platform(runtime.GOOS)
	if options.platform != "" {
		platform = service.Platform(options.platform)
	}
	spec, err := service.ValidateSpec(options.spec, platform)
	if err != nil {
		return reportError(stderr, err)
	}
	if action == "render" {
		definition, err := service.Render(spec, platform)
		if err != nil {
			return reportError(stderr, err)
		}
		if options.json {
			return writeJSON(stdout, stderr, definition)
		}
		fmt.Fprint(stdout, definition.Content)
		return 0
	}
	if options.platform != "" && service.Platform(options.platform) != service.Platform(runtime.GOOS) {
		fmt.Fprintf(stderr, "ctx: service lifecycle runs on %s; render a %s definition instead\n", runtime.GOOS, options.platform)
		return 2
	}
	if options.directory == "" {
		fmt.Fprintln(stderr, "ctx: service lifecycle needs --dir for the definition directory")
		return 2
	}
	manager, err := currentServiceManager(options.directory)
	if err != nil {
		return reportError(stderr, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	switch action {
	case "status":
		status, err := manager.Status(ctx, spec)
		if err != nil {
			return reportError(stderr, err)
		}
		if options.json {
			return writeJSON(stdout, stderr, status)
		}
		fmt.Fprintf(stdout, "%s: installed=%t registered=%t active=%t stale=%t\n%s\n",
			status.Name, status.Installed, status.Registered, status.Active, status.Stale, status.Detail)
		return 0
	case "register":
		registration, err := manager.Register(ctx, spec)
		if err != nil {
			return reportError(stderr, err)
		}
		if options.json {
			return writeJSON(stdout, stderr, registration)
		}
		fmt.Fprintf(stdout, "registered %s at %s\n", registration.Definition.Identity, registration.Publication.Path)
		return 0
	case "unregister":
		if err := manager.Unregister(ctx, spec); err != nil {
			return reportError(stderr, err)
		}
		fmt.Fprintf(stdout, "unregistered %s\n", spec.Name)
		return 0
	case "start":
		if err := manager.Start(ctx, spec); err != nil {
			return reportError(stderr, err)
		}
		fmt.Fprintf(stdout, "started %s\n", spec.Name)
		return 0
	case "stop":
		if err := manager.Stop(ctx, spec); err != nil {
			return reportError(stderr, err)
		}
		fmt.Fprintf(stdout, "stopped %s\n", spec.Name)
		return 0
	}
	return 2
}

type serviceOptions struct {
	spec      service.Spec
	directory string
	platform  string
	json      bool
}

func parseServiceOptions(args []string) (serviceOptions, error) {
	options := serviceOptions{}
	for index := 0; index < len(args); index++ {
		argument := args[index]
		name, inline, hasInline := strings.Cut(argument, "=")
		value := func() (string, error) {
			if hasInline {
				return inline, nil
			}
			if index+1 >= len(args) || strings.HasPrefix(args[index+1], "--") {
				return "", fmt.Errorf("%s needs a value", name)
			}
			index++
			return args[index], nil
		}
		switch name {
		case "--name":
			item, err := value()
			if err != nil {
				return options, err
			}
			options.spec.Name = item
		case "--executable":
			item, err := value()
			if err != nil {
				return options, err
			}
			options.spec.Executable = item
		case "--description":
			item, err := value()
			if err != nil {
				return options, err
			}
			options.spec.Description = item
		case "--arg":
			item, err := value()
			if err != nil {
				return options, err
			}
			options.spec.Arguments = append(options.spec.Arguments, item)
		case "--env":
			item, err := value()
			if err != nil {
				return options, err
			}
			key, environment, ok := strings.Cut(item, "=")
			if !ok || key == "" {
				return options, fmt.Errorf("--env needs KEY=VALUE")
			}
			if options.spec.Environment == nil {
				options.spec.Environment = map[string]string{}
			}
			options.spec.Environment[key] = environment
		case "--workdir":
			item, err := value()
			if err != nil {
				return options, err
			}
			options.spec.WorkingDirectory = item
		case "--restart":
			value, err := value()
			if err != nil {
				return options, err
			}
			options.spec.Restart = service.RestartPolicy(value)
		case "--restart-seconds":
			value, err := value()
			if err != nil {
				return options, err
			}
			seconds, err := strconv.Atoi(value)
			if err != nil {
				return options, fmt.Errorf("--restart-seconds needs an integer")
			}
			options.spec.RestartSeconds = seconds
		case "--user":
			item, err := value()
			if err != nil {
				return options, err
			}
			options.spec.UserID = item
		case "--dir":
			item, err := value()
			if err != nil {
				return options, err
			}
			options.directory = item
		case "--platform":
			item, err := value()
			if err != nil {
				return options, err
			}
			options.platform = item
		case "--json":
			if hasInline {
				return options, fmt.Errorf("--json takes no value")
			}
			options.json = true
		default:
			return options, fmt.Errorf("unknown service option %s", name)
		}
	}
	return options, nil
}

// currentServiceManager implements the platform's own service mechanism. Linux
// systemd is a selectable native tool delivered as adapters/systemd, which ctx
// core does not link; render a unit with --platform linux and register it with
// that library or the system's own tooling.
func currentServiceManager(directory string) (service.Manager, error) {
	switch runtime.GOOS {
	case "darwin":
		return launchd.New(launchd.Config{Directory: directory, UID: os.Getuid()})
	case "windows":
		return taskscheduler.New(taskscheduler.Config{Directory: directory})
	default:
		return nil, fmt.Errorf("service lifecycle on %s requires the adapters/systemd library; use ctx service render --platform linux to generate a unit", runtime.GOOS)
	}
}

func writeJSON(stdout io.Writer, stderr io.Writer, value any) int {
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return reportError(stderr, err)
	}
	return 0
}
