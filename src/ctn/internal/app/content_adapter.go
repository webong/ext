package app

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/webong/ext/pkg/ext-go/engine/plugin/host/adapter"
	"github.com/webong/ext/src/ctn/internal/content"
	"github.com/webong/ext/src/cto/sandbox"
)

// evaluatorBackend wraps the selected evaluator adapter as a content.Backend.
type evaluatorBackend struct {
	adapter *adapter.Adapter
	prefix  string
}

func (b *evaluatorBackend) Name() string {
	return b.adapter.Manifest.Name
}

func (b *evaluatorBackend) Run(ctx context.Context, file, selection string, timeout time.Duration, admit func(sandbox.Report) error) (int, []byte, error) {
	command, err := b.adapter.CommandContext(ctx, adapter.Invocation{
		Operation: sandbox.AdmissionOperation,
		Selection: selection,
		Arguments: []string{"--timeout", timeout.String(), "--", b.prefix + file},
	})
	if err != nil {
		return 0, nil, err
	}
	command.WaitDelay = time.Second

	stdin, err := command.StdinPipe()
	if err != nil {
		return 0, nil, err
	}
	defer stdin.Close()
	stdout, err := command.StdoutPipe()
	if err != nil {
		return 0, nil, err
	}
	defer stdout.Close()
	var diagnostics evaluatorOutput
	command.Stderr = &diagnostics
	if err := command.Start(); err != nil {
		return 0, nil, err
	}

	// A descendant could inherit stdout. Cancellation closes the parent pipes
	// independently of how long it takes for the adapter's stdout to reach EOF.
	stop := context.AfterFunc(ctx, func() {
		stdin.Close()
		stdout.Close()
	})
	defer stop()

	reader := bufio.NewReader(stdout)
	report, err := sandbox.ReadAdmissionReport(reader)
	if errors.Is(err, io.EOF) {
		err = errors.New("runner supplied no sandbox evidence")
	}
	if err == nil {
		err = admit(report)
	}
	if err == nil {
		err = ctx.Err()
	}
	if err == nil {
		_, err = io.WriteString(stdin, sandbox.AdmissionApproval)
	}
	stdin.Close()
	if err != nil {
		stdout.Close()
		command.Process.Kill()
		command.Wait()
		if detail := strings.TrimSpace(string(diagnostics)); detail != "" {
			return 0, nil, fmt.Errorf("%w: %s", err, detail)
		}
		return 0, nil, err
	}

	var output evaluatorOutput
	_, readErr := io.Copy(&output, reader)
	if err := command.Wait(); err != nil {
		output.Write(diagnostics)
		return command.ProcessState.ExitCode(), output, err
	}
	return 0, output, readErr
}

// evaluatorOutput retains a bounded prefix and keeps draining the pipe.
type evaluatorOutput []byte

func (output *evaluatorOutput) Write(data []byte) (int, error) {
	const limit = 4096
	n := min(len(data), limit-len(*output))
	*output = append(*output, data[:n]...)
	return len(data), nil
}

type evaluatorPolicy struct {
	store *adapter.Store
}

func (p *evaluatorPolicy) IsTrusted(backend content.Backend) bool {
	b, ok := backend.(*evaluatorBackend)
	return ok && p.store.AssertTrusted(b.adapter) == nil
}

func (p *evaluatorPolicy) Admit(report sandbox.Report) error {
	if err := report.Validate(); err != nil {
		return fmt.Errorf("invalid sandbox evidence: %w", err)
	}
	if !sandbox.Sandboxed(report) {
		return fmt.Errorf("runner boundary %s and enforcement do not meet sandbox requirements", report.Boundary)
	}
	// Verification requests fresh execution with no access to the host.
	// An enforced grant can still allow access CTN never requested.
	grant := report.Grant
	if len(grant.Filesystem) != 0 || grant.Process.Children || len(grant.Devices) != 0 ||
		len(grant.Environment.Names)+len(grant.Environment.Entries)+len(grant.Environment.Secrets) != 0 ||
		(grant.Network.Mode != "" && grant.Network.Mode != sandbox.NetworkNone) ||
		(grant.Lifecycle.Mode != "" && grant.Lifecycle.Mode != sandbox.LifecycleFresh) {
		return errors.New("runner sandbox grant exceeds verification policy")
	}
	return nil
}
