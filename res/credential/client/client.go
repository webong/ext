// Package client reads credentials through CTX's installed adapter protocol.
// It has no dependency on a native store implementation.
package client

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

const maxValueBytes = 1024 * 1024

var validName = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
var validEnvironment = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

type boundedOutput struct{ bytes.Buffer }

func (output *boundedOutput) Write(value []byte) (int, error) {
	if output.Len()+len(value) > maxValueBytes {
		return 0, errors.New("credential exceeds 1 MiB")
	}
	return output.Buffer.Write(value)
}

// GetDeclared asks CTX for an item in the adapter declared for a share space
// by the caller's manifest. The host supplies the dependency binding when it
// starts the caller; values never appear in arguments or configuration.
func GetDeclared(ctx context.Context, space, item string) ([]byte, error) {
	return GetDeclaredWithEnv(ctx, space, item, nil)
}

// GetDeclaredWithEnv passes non-secret native selector settings to the host.
// Callers must not use this for credential values.
func GetDeclaredWithEnv(ctx context.Context, space, item string, extra map[string]string) ([]byte, error) {
	if !validName.MatchString(space) {
		return nil, errors.New("invalid credential dependency space")
	}
	name := os.Getenv("CTX_DEPENDENCY_" + strings.ToUpper(space))
	if !validName.MatchString(name) {
		return nil, fmt.Errorf("%s credential dependency is not declared; install the store adapter and run through ctx", space)
	}
	if item == "" || len(item) > 2048 || strings.ContainsAny(item, "\x00\r\n") {
		return nil, errors.New("invalid credential item")
	}
	executable := os.Getenv("CTX_EXECUTABLE")
	if executable == "" {
		var err error
		executable, err = exec.LookPath("ctx")
		if err != nil {
			return nil, errors.New("ctx executable is unavailable for credential lookup")
		}
	}
	command := exec.CommandContext(ctx, executable, "credential", "get", name+":"+item, "--stdout")
	if len(extra) > 0 {
		command.Env = os.Environ()
		for key, value := range extra {
			if !validEnvironment.MatchString(key) || key == "CTX_EXECUTABLE" ||
				strings.HasPrefix(key, "CTX_DEPENDENCY_") || strings.ContainsRune(value, 0) {
				return nil, errors.New("invalid credential lookup environment")
			}
			command.Env = append(command.Env, key+"="+value)
		}
	}
	var output boundedOutput
	command.Stdout = &output
	command.Stderr = os.Stderr
	if err := command.Run(); err != nil {
		return nil, fmt.Errorf("credential lookup via %s failed: %w; check that the dependency is installed and trusted", name, err)
	}
	if output.Len() == 0 {
		return nil, fmt.Errorf("credential adapter %s returned an empty item", name)
	}
	return output.Bytes(), nil
}
