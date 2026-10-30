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
)

// Verdict is the outcome of content verification.
type Verdict string

const (
	VerdictPass      Verdict = "PASS"
	VerdictFail      Verdict = "FAIL"
	VerdictUnsupported Verdict = "UNSUPPORTED"
	VerdictTimeout   Verdict = "TIMEOUT"
)

// Exit codes returned by verification.
const (
	ExitPass        = 0
	ExitFail        = 1
	ExitUsage       = 2
	ExitUnverified  = 3
	ExitTimeout     = 124
)

// MaxContentBytes bounds the size of content that can be verified.
const MaxContentBytes = 256 << 20

// Result is the structured outcome of content verification.
type Result struct {
	Verdict   Verdict `json:"verdict"`
	File      string  `json:"file"`
	SHA256    string  `json:"sha256"`
	Backend   string  `json:"backend,omitempty"`
	Selection string  `json:"selection,omitempty"`
	Isolation string  `json:"isolation,omitempty"`
	ExitCode  int     `json:"exitCode"`
	Millis    int64   `json:"millis"`
	Reason    string  `json:"reason,omitempty"`
}

// Backend is a caller-selected execution backend for running content.
type Backend interface {
	// Name returns the backend identity.
	Name() string
	// Run executes the content with the given arguments and returns the exit
	// code, captured output, and any error. A non-nil error with a non-zero
	// exit code indicates the content itself failed; a non-nil error with a
	// zero exit code indicates an infrastructure failure.
	Run(ctx context.Context, arguments []string) (exitCode int, output []byte, err error)
}

// Policy decides whether content may run through a given backend.
type Policy interface {
	// IsTrusted reports whether the backend is trusted to run content.
	IsTrusted(backend Backend) bool
	// Isolation returns the isolation level for the backend/selection, a
	// human-readable reason, and whether the level permits execution.
	Isolation(backend Backend, selection string) (level string, reason string, ok bool)
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

	// Isolation check with the caller's selection.
	level, reason, ok := policy.Isolation(backend, selection)
	if !ok {
		result.Verdict = VerdictUnsupported
		result.Isolation = level
		result.Reason = reason
		if observer != nil {
			observer.Completed(result)
		}
		return result, nil
	}
	result.Isolation = level

	// Run with an outer timeout. Pass the file and selection as arguments so
	// the backend can construct the appropriate invocation. The file is
	// always first so backends can apply path prefixes (for example "@" for
	// evm) to it.
	runCtx, cancel := context.WithTimeout(ctx, timeout+5*time.Second)
	defer cancel()

	if observer != nil {
		observer.Started(backend.Name())
	}

	started := time.Now()
	args := []string{file}
	if selection != "" {
		args = append(args, selection)
	}
	args = append(args, "--timeout", timeout.String())
	exitCode, output, runErr := backend.Run(runCtx, args)
	result.Millis = time.Since(started).Milliseconds()
	result.ExitCode = exitCode

	if runErr != nil {
		var exit *exec.ExitError
		switch {
		case errors.Is(runCtx.Err(), context.DeadlineExceeded):
			result.Verdict = VerdictTimeout
			result.Reason = fmt.Sprintf("did not finish within %s", timeout)
		case errors.As(runErr, &exit):
			result.ExitCode = exit.ExitCode()
			result.Verdict = classifyExitCode(exit.ExitCode(), string(output))
			result.Reason = string(truncateOutput(output, 400))
		default:
			result.Verdict = VerdictUnsupported
			result.Reason = runErr.Error()
		}
	} else {
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
