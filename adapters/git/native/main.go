package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"

	"github.com/webong/ext/pkg/plugin"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	request, err := plugin.ParseAdapterInvocation(args)
	if err != nil {
		fmt.Fprintf(stderr, "git: %v\n", err)
		return 2
	}
	git, err := gitExecutable()
	if err != nil {
		fmt.Fprintf(stderr, "git: %v\n", err)
		return 127
	}
	switch request.Operation {
	case "validate":
		return 0
	case "doctor":
		return forward(git, []string{"--version"}, stdin, stdout, stderr)
	case "run":
		if len(request.Arguments) > 0 && request.Arguments[0] == "hooks" {
			if err := hooks(git, request.Arguments[1:], stdout); err != nil {
				fmt.Fprintf(stderr, "git: %v\n", err)
				return 1
			}
			return 0
		}
		return forward(git, request.Arguments, stdin, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "git: unsupported adapter operation %s\n", request.Operation)
		return 2
	}
}

func gitExecutable() (string, error) {
	if real := os.Getenv("CTX_ADAPTER_REAL_COMMAND"); real != "" {
		info, err := os.Stat(real)
		if err != nil || !info.Mode().IsRegular() {
			return "", errors.New("native Git executable is unavailable")
		}
		return real, nil
	}
	git, err := exec.LookPath("git")
	if err != nil {
		return "", errors.New("Git is not installed")
	}
	return git, nil
}

func forward(git string, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	command := exec.Command(git, args...)
	command.Stdin, command.Stdout, command.Stderr = stdin, stdout, stderr
	if err := command.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return exit.ExitCode()
		}
		fmt.Fprintf(stderr, "git: %v\n", err)
		return 1
	}
	return 0
}
