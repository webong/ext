package app

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/webong/ext/pkg/graph/system"
)

func graphProcessCommand(ctx context.Context, system *systemgraph.Graph, command string, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("graph "+command, flag.ContinueOnError)
	flags.SetOutput(stderr)
	options := systemgraph.ProcessOptions{MaxProcesses: 4096, MaxResources: 1024}
	if command == "process" {
		flags.IntVar(&options.MaxResources, "resource-limit", 1024, "maximum records per resource category")
	} else {
		flags.IntVar(&options.MaxProcesses, "limit", 4096, "maximum processes in an inventory")
	}
	flags.DurationVar(&options.Timeout, "timeout", 10*time.Second, "collection deadline")
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		fmt.Fprintf(stdout, "ctx graph %s", command)
		if command == "process" {
			fmt.Fprint(stdout, " <pid>")
		}
		fmt.Fprintln(stdout, " [options]")
		flags.SetOutput(stdout)
		flags.PrintDefaults()
		return 0
	}
	pid := 0
	if command == "process" {
		if len(args) == 0 {
			fmt.Fprintln(stderr, "ctx: usage: graph process <pid> [--resource-limit 1024] [--timeout 10s]")
			return 2
		}
		var err error
		pid, err = strconv.Atoi(args[0])
		if err != nil || pid <= 0 || uint64(pid) > 1<<31-1 {
			fmt.Fprintln(stderr, "ctx: process PID must be a positive 32-bit integer")
			return 2
		}
		args = args[1:]
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 || options.MaxProcesses <= 0 || options.MaxProcesses > 65536 || options.MaxResources <= 0 || options.MaxResources > 65536 || options.Timeout <= 0 {
		fmt.Fprintln(stderr, "ctx: process limits must be between 1 and 65536, timeout must be positive, and extra arguments are not accepted")
		return 2
	}
	var observation systemgraph.ProcessSnapshot
	var err error
	if command == "process" {
		observation, err = systemgraph.InspectProcess(ctx, pid, options)
	} else {
		observation, err = systemgraph.DiscoverProcesses(ctx, options)
	}
	if err != nil {
		return reportError(stderr, err)
	}
	if err = system.ObserveProcesses(ctx, observation); err != nil {
		return reportError(stderr, err)
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	if err = encoder.Encode(observation); err != nil {
		return reportError(stderr, err)
	}
	return 0
}
