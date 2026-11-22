// Package content implements CTN's verification workflow and durable file catalog.
// The app package supplies adapter selection, trust policy, and CLI output.
package content

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/webong/ext/src/cto/sandbox"
)

// Verdict is the outcome of content verification.
type Verdict string

const (
	VerdictPass        Verdict = "PASS"
	VerdictFail        Verdict = "FAIL"
	VerdictUnsupported Verdict = "UNSUPPORTED"
	VerdictTimeout     Verdict = "TIMEOUT"
)

// Exit codes returned by verification.
const (
	ExitPass       = 0
	ExitFail       = 1
	ExitUsage      = 2
	ExitUnverified = 3
	ExitTimeout    = 124
)

// MaxContentBytes bounds the size of content that can be verified.
const MaxContentBytes = 256 << 20

// Result is the structured outcome of content verification.
type Result struct {
	Verdict   Verdict         `json:"verdict"`
	File      string          `json:"file"`
	SHA256    string          `json:"sha256"`
	Backend   string          `json:"backend,omitempty"`
	Selection string          `json:"selection,omitempty"`
	Isolation string          `json:"isolation,omitempty"`
	Boundary  string          `json:"boundary,omitempty"`
	Sandbox   *sandbox.Report `json:"sandbox,omitempty"`
	ExitCode  int             `json:"exitCode"`
	Millis    int64           `json:"millis"`
	Reason    string          `json:"reason,omitempty"`
}

// Backend is a caller-selected execution backend for running content.
type Backend interface {
	// Name returns the backend identity.
	Name() string
	// Run requests admission from the caller after preparing the runner and
	// before executing content. An admission error prevents execution.
	// A non-nil error with a non-zero
	// exit code indicates the content itself failed; a non-nil error with a
	// zero exit code indicates an infrastructure failure.
	Run(ctx context.Context, file, selection string, timeout time.Duration, admit func(sandbox.Report) error) (exitCode int, output []byte, err error)
}

// Policy decides whether content may run through a given backend.
type Policy interface {
	// IsTrusted reports whether the backend is trusted to run content.
	IsTrusted(backend Backend) bool
	// Admit checks the prepared runner's report against the consumer's policy.
	Admit(report sandbox.Report) error
}

// Limits bound resource usage during verification.
type Limits struct {
	// MaxOutputBytes bounds the captured output size.
	MaxOutputBytes int64
}

// Observer optionally receives verification events.
type Observer interface {
	// Started is called when verification begins.
	Started(backend string)
	// Completed is called when verification ends with the final result.
	Completed(result *Result)
}

// Verify runs content through a backend under a policy and returns a
// structured result. The caller supplies the backend, policy, limits, and
// optional observer. Content larger than MaxContentBytes is rejected.
func Verify(ctx context.Context, file string, selection string, backend Backend, policy Policy, limits Limits, timeout time.Duration, observer Observer) (*Result, error) {
	if backend == nil {
		return nil, errors.New("backend is required")
	}
	if policy == nil {
		return nil, errors.New("policy is required")
	}

	result := &Result{File: file, Selection: selection}

	// Compute SHA-256 of the content.
	data, err := os.ReadFile(file)
	if err != nil {
		result.Verdict = VerdictUnsupported
		result.Reason = err.Error()
		if observer != nil {
			observer.Completed(result)
		}
		return result, nil
	}
	if int64(len(data)) > MaxContentBytes {
		result.Verdict = VerdictUnsupported
		result.Reason = fmt.Sprintf("content is larger than %d bytes", MaxContentBytes)
		if observer != nil {
			observer.Completed(result)
		}
		return result, nil
	}
	sum := sha256.Sum256(data)
	result.SHA256 = hex.EncodeToString(sum[:])

	// Trust check.
	if !policy.IsTrusted(backend) {
		result.Verdict = VerdictUnsupported
		result.Backend = backend.Name()
		result.Reason = fmt.Sprintf("backend %s is not trusted", backend.Name())
		if observer != nil {
			observer.Completed(result)
		}
		return result, nil
	}
	result.Backend = backend.Name()

	// The timeout includes runner preparation, admission, and execution.
	runCtx, cancel := context.WithTimeout(ctx, timeout+5*time.Second)
	defer cancel()

	admit := func(report sandbox.Report) error {
		if err := policy.Admit(report); err != nil {
			return err
		}
		result.Isolation = "sandboxed"
		result.Boundary = string(report.Boundary)
		result.Sandbox = &report
		if observer != nil {
			observer.Started(backend.Name())
		}
		return nil
	}

	started := time.Now()
	exitCode, output, runErr := backend.Run(runCtx, file, selection, timeout, admit)
	result.Millis = time.Since(started).Milliseconds()
	result.ExitCode = exitCode

	switch {
	case errors.Is(runCtx.Err(), context.DeadlineExceeded):
		result.Verdict = VerdictTimeout
		result.Reason = fmt.Sprintf("did not finish within %s", timeout)
	case result.Sandbox == nil:
		result.Verdict = VerdictUnsupported
		result.Reason = "runner supplied no accepted sandbox evidence"
		if runErr != nil {
			result.Reason = runErr.Error()
		}
	case runErr != nil:
		var exit *exec.ExitError
		switch {
		case errors.As(runErr, &exit):
			result.ExitCode = exit.ExitCode()
			result.Verdict = classifyExitCode(exit.ExitCode(), string(output))
			result.Reason = string(truncateOutput(output, 400))
		default:
			result.Verdict = VerdictUnsupported
			result.Reason = runErr.Error()
		}
	default:
		result.Verdict = classifyExitCode(exitCode, string(output))
		result.Reason = string(truncateOutput(output, 400))
	}

	if observer != nil {
		observer.Completed(result)
	}
	return result, nil
}

func classifyExitCode(code int, output string) Verdict {
	switch code {
	case 0:
		return VerdictPass
	case ExitTimeout:
		return VerdictTimeout
	case 126, 127:
		return VerdictUnsupported
	default:
		return VerdictFail
	}
}

func truncateOutput(output []byte, max int) []byte {
	if len(output) > max {
		return append(output[:max], []byte("…")...)
	}
	return output
}
