package adaptersnapshot

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"strings"
	"time"

	plugin "github.com/webong/ext/pkg/ext-go/cgoengine/plugin"
	"github.com/webong/ext/pkg/ext-go/cgoengine/plugin/host/commandscope"
	adapter "github.com/webong/ext/pkg/ext-go/engine/plugin/host/adapter"
	"github.com/webong/ext/src/ctx/internal/adapterenv"
)

// CTX owns this command policy. The broker itself knows neither credentials nor
// adapters. This is the existing read-only declared credential shell workflow.
func dispatch(ctx context.Context, s *commandscope.Scope, consumer plugin.Identity, request commandscope.Request) (commandscope.Result, error) {
	args := request.Args
	if len(args) > 0 && args[0] == "ctx" {
		args = args[1:]
	}
	if len(args) != 4 || args[0] != "credential" || args[1] != "get" || args[3] != "--stdout" || len(request.Input) != 0 {
		return commandscope.Result{}, plugin.ErrDenied
	}
	name, item, ok := strings.Cut(args[2], ":")
	if !ok || name == "" || item == "" || len(item) > 2048 || strings.ContainsAny(item, "\x00\r\n") {
		return commandscope.Result{}, plugin.ErrDenied
	}
	source, err := s.Load(consumer)
	if err != nil {
		return commandscope.Result{}, err
	}
	a, err := adapter.LoadDirectory(source.Directory)
	if err != nil {
		return commandscope.Result{}, err
	}
	dep, ok := a.Manifest.Dependencies["credential"]
	if !ok || dep.Adapter != name {
		return commandscope.Result{}, plugin.ErrDenied
	}
	var provider plugin.Identity
	for _, b := range s.Plan.Bindings {
		if b.Consumer == consumer && b.Provider.ID == name && b.Requirement.Contract == (plugin.ContractRef{Name: plugin.AdapterContractName, Version: dep.APIVersion}) && b.Requirement.Operation == "share" {
			provider = b.Provider
		}
	}
	if provider.ID == "" {
		return commandscope.Result{}, plugin.ErrDenied
	}
	installed, err := s.Load(provider)
	if err != nil {
		return commandscope.Result{}, err
	}
	target, err := adapter.LoadDirectory(installed.Directory)
	if err != nil {
		return commandscope.Result{}, err
	}
	if err := adapter.ValidateDependency(dep, target); err != nil {
		return commandscope.Result{}, err
	}
	command, err := target.CommandContext(ctx, adapter.Invocation{Operation: "share", Arguments: []string{"credential", "get", item}})
	if err != nil {
		return commandscope.Result{}, err
	}
	command.Env = s.WithEnvironment(adapterenv.Dependencies(adapterenv.Host(command.Env), target.Manifest.Dependencies), provider)
	command.WaitDelay = 2 * time.Second
	output, stderr := &bounded{}, &bounded{}
	command.Stdout, command.Stderr = output, stderr
	err = command.Run()
	result := commandscope.Result{Output: output.Bytes(), Error: stderr.Bytes()}
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			result.Exit = exit.ExitCode()
			if result.Exit < 0 {
				result.Exit = 1
			}
		} else {
			return result, err
		}
	}
	if ctx.Err() != nil {
		return commandscope.Result{}, ctx.Err()
	}
	return result, nil
}

type bounded struct{ bytes.Buffer }

func (b *bounded) Write(p []byte) (int, error) {
	if b.Len()+len(p) > commandscope.MaxBytes {
		return 0, errors.New("nested command output exceeds limit")
	}
	return b.Buffer.Write(p)
}
