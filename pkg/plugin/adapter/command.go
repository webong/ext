package adapter

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

type Invocation struct {
	Operation   string
	Selection   string
	Arguments   []string
	Values      map[string]string
	Profile     string
	Project     string
	Command     string
	RealCommand string
}

func (a *Adapter) Command(invocation Invocation) (*exec.Cmd, error) {
	return a.command(nil, invocation)
}

// CommandContext prepares an adapter invocation whose process is cancelled
// when ctx is done. A nil context retains Command's behavior.
func (a *Adapter) CommandContext(ctx context.Context, invocation Invocation) (*exec.Cmd, error) {
	return a.command(ctx, invocation)
}

func (a *Adapter) command(ctx context.Context, invocation Invocation) (*exec.Cmd, error) {
	if !a.HasCapability(invocation.Operation) {
		return nil, fmt.Errorf("adapter %s does not support %s", a.Manifest.Name, invocation.Operation)
	}
	var args []string
	switch invocation.Operation {
	case "list", "observe":
		args = []string{invocation.Operation}
	case "validate", "doctor":
		args = []string{invocation.Operation, invocation.Selection}
	default:
		args = []string{invocation.Operation}
		if invocation.Selection != "" || !a.IsComputerEndpoint() {
			args = append(args, invocation.Selection)
		}
		args = append(args, "--")
		args = append(args, invocation.Arguments...)
	}
	command := adapterCommandContext(ctx, a.ExecutablePath(), args)
	command.Env = os.Environ()
	command.Env = setEnvironment(command.Env, "CTX_ADAPTER_API", a.Manifest.APIVersion)
	command.Env = setEnvironment(command.Env, "CTX_ADAPTER_NAME", a.Manifest.Name)
	requestedCommand := invocation.Command
	if requestedCommand == "" {
		requestedCommand = a.Manifest.Name
	}
	command.Env = setEnvironment(command.Env, "CTX_ADAPTER_COMMAND", requestedCommand)
	command.Env = setEnvironment(command.Env, "CTX_ADAPTER_REAL_COMMAND", invocation.RealCommand)
	command.Env = setEnvironment(command.Env, "CTX_PROJECT_DIR", invocation.Project)
	command.Env = setEnvironment(command.Env, "CTX_PROFILE", invocation.Profile)
	for key, value := range invocation.Values {
		environmentKey := "CTX_ADAPTER_VALUE_" + strings.ToUpper(key)
		command.Env = setEnvironment(command.Env, environmentKey, value)
	}
	return command, nil
}

func setEnvironment(values []string, key, value string) []string {
	prefix := strings.ToUpper(key) + "="
	for index, entry := range values {
		if strings.HasPrefix(strings.ToUpper(entry), prefix) {
			values[index] = key + "=" + value
			return values
		}
	}
	return append(values, key+"="+value)
}
