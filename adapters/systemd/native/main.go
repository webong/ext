package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/webong/ext/pkg/ext-go/cgoengine"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	request, err := cgoengine.ParseAdapterInvocation(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "systemd: %v\n", err)
		return 2
	}
	switch request.Operation {
	case "validate":
		return 0
	case "doctor":
		return forward([]string{"--user", "--version"})
	case "run":
		if err := validateRun(request.Arguments); err != nil {
			fmt.Fprintf(os.Stderr, "systemd: %v\n", err)
			return 2
		}
		return forward(append([]string{"--user"}, request.Arguments...))
	default:
		fmt.Fprintf(os.Stderr, "systemd: unsupported adapter operation %s\n", request.Operation)
		return 2
	}
}

// runOperations is the closed set of read-only and lifecycle verbs this adapter
// forwards to the user manager. It never accepts an arbitrary command line.
var runOperations = map[string]bool{
	"status": true, "show": true, "cat": true, "list-units": true, "list-unit-files": true,
	"is-active": true, "is-enabled": true, "start": true, "stop": true, "restart": true,
	"enable": true, "disable": true, "daemon-reload": true,
}

func validateRun(args []string) error {
	if len(args) == 0 {
		return errors.New("run requires a user-manager operation")
	}
	if !runOperations[args[0]] {
		return fmt.Errorf("unsupported user-manager operation %q", args[0])
	}
	for _, argument := range args[1:] {
		if argument == "" || len(argument) > 256 || strings.HasPrefix(argument, "-") ||
			strings.ContainsAny(argument, "/\\\x00\r\n") {
			return errors.New("invalid user-manager argument")
		}
	}
	return nil
}

func forward(args []string) int {
	command := exec.Command("systemctl", args...)
	command.Stdin, command.Stdout, command.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := command.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "systemd: systemctl failed: %v\n", err)
		return 1
	}
	return 0
}
