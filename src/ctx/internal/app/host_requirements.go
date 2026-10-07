package app

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/webong/ctx/pkg/graph/system"
)

func prepareHostOperation(requirements systemgraph.OperationRequirements) (systemgraph.PreparedHost, error) {
	system, err := systemgraph.Open(configHomePath())
	if err != nil {
		return systemgraph.PreparedHost{}, err
	}
	defer system.Close()
	return system.PrepareHost(context.Background(), requirements, nil)
}

func prepareOutputDirectory(operation, directory string, minimumBytes uint64) error {
	info, err := os.Stat(directory)
	if err != nil {
		return fmt.Errorf("%s: output directory: %w", operation, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s: output parent is not a directory", operation)
	}
	_, err = prepareHostOperation(systemgraph.OperationRequirements{
		Operation:  operation,
		Filesystem: &systemgraph.FilesystemRequirement{Path: directory, Writable: true, MinimumAvailableBytes: minimumBytes},
	})
	return err
}

type filesystemTypes []string

func (types *filesystemTypes) String() string { return fmt.Sprint([]string(*types)) }
func (types *filesystemTypes) Set(value string) error {
	if value == "" {
		return fmt.Errorf("filesystem type cannot be empty")
	}
	*types = append(*types, value)
	return nil
}

func graphHostResolve(system *systemgraph.Graph, kind string, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("graph resolve "+kind, flag.ContinueOnError)
	flags.SetOutput(stderr)
	selectValue := flags.String("select", "", "explicit executable, mount point, or runtime location")
	maxAge := flags.Duration("max-age", 0, "maximum observation age (default 5s; negative forces refresh)")
	requirements := systemgraph.OperationRequirements{Operation: "graph.resolve." + kind}
	shell := systemgraph.ShellRequirement{}
	filesystem := systemgraph.FilesystemRequirement{}
	webview := systemgraph.WebviewRequirement{}
	var types filesystemTypes
	switch kind {
	case "shell":
		flags.StringVar(&shell.Name, "name", "", "required shell name")
		requirements.Shell = &shell
	case "filesystem":
		flags.StringVar(&filesystem.Path, "path", "", "destination path, including paths not created yet")
		flags.Var(&types, "type", "required filesystem type (repeat for alternatives)")
		flags.BoolVar(&filesystem.Writable, "writable", false, "require a mount that is not read-only")
		flags.Uint64Var(&filesystem.MinimumAvailableBytes, "min-free", 0, "minimum available bytes")
		requirements.Filesystem = &filesystem
	case "webview":
		flags.StringVar(&webview.Engine, "engine", "", "required rendering engine")
		flags.StringVar(&webview.API, "api", "", "required embedding API")
		flags.StringVar(&webview.ABIVersion, "abi", "", "required API/ABI generation")
		flags.StringVar(&webview.Version, "version", "", "required exact runtime version")
		flags.StringVar(&webview.Architecture, "arch", "", "required architecture identifier")
		requirements.Webview = &webview
	default:
		return reportErrorCode(stderr, fmt.Errorf("unknown host requirement %s", kind), 2)
	}
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if len(flags.Args()) != 0 {
		return reportErrorCode(stderr, fmt.Errorf("graph resolve %s accepts flags only", kind), 2)
	}
	requirements.MaxAge = *maxAge
	switch kind {
	case "shell":
		shell.Select = *selectValue
	case "filesystem":
		filesystem.Select, filesystem.Types = *selectValue, types
		if filesystem.Select != "" {
			absolute, err := filepath.Abs(filesystem.Select)
			if err != nil {
				return reportErrorCode(stderr, err, 2)
			}
			filesystem.Select = absolute
		}
	case "webview":
		webview.Select = *selectValue
	}
	result, err := system.ResolveHost(context.Background(), requirements)
	if err != nil {
		return reportError(stderr, err)
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		return reportError(stderr, err)
	}
	return 0
}
