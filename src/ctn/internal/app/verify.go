package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/webong/ext/pkg/plugin/adapter"
)

// Exit statuses of verify. They are separate so a script can tell content that
// failed from content that could not be checked.
const (
	exitPass        = 0
	exitFail        = 1
	exitUsage       = 2
	exitUnverified  = 3
	exitTimeout     = 124
	maxContentBytes = 256 << 20
)

// kinds maps a file extension to the engine kind that runs it, and says how the
// engine wants the file named: the evm engine reads a file given as @path.
var kinds = map[string]struct{ kind, prefix string }{
	".wasm": {"wasm", ""},
	".evm":  {"evm", "@"},
	".hex":  {"evm", "@"},
}

// verdict is what verify reports.
type verdict struct {
	Verdict   string `json:"verdict"`
	File      string `json:"file"`
	SHA256    string `json:"sha256"`
	Engine    string `json:"engine,omitempty"`
	Selection string `json:"selection,omitempty"`
	Isolation string `json:"isolation,omitempty"`
	ExitCode  int    `json:"exitCode"`
	Millis    int64  `json:"millis"`
	Reason    string `json:"reason,omitempty"`
}

// verifyCommand runs content through a sandboxed engine and reports whether it
// ran cleanly. The content is never run by an engine that does not confine it.
func verifyCommand(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("ctn verify", flag.ContinueOnError)
	flags.SetOutput(stderr)
	engineName := flags.String("engine", "", "the engine adapter to use (default: chosen by the file's extension)")
	selection := flags.String("selection", "", "the engine selection to use (for example a rule set or a runtime)")
	timeout := flags.Duration("timeout", 30*time.Second, "stop the content after this long")
	asJSON := flags.Bool("json", false, "print the verdict as one JSON object")
	if err := flags.Parse(args); err != nil {
		return exitUsage
	}
	rest := flags.Args()
	if len(rest) == 0 || *timeout <= 0 {
		fmt.Fprintln(stderr, "ctn: usage: ctn verify [--engine NAME] [--selection SEL] [--timeout D] [--json] FILE [ARGUMENTS...]")
		return exitUsage
	}
	file := rest[0]
	report := verdict{File: file}
	// finish prints the verdict and returns ctn's own exit status. The status the
	// content itself exited with is report.ExitCode, set only once it has run.
	finish := func(code int, name, reason string) int {
		report.Verdict, report.Reason = name, reason
		printVerdict(stdout, report, *asJSON)
		return code
	}

	content, err := os.ReadFile(file)
	if err != nil {
		fmt.Fprintf(stderr, "ctn: %v\n", err)
		return exitUsage
	}
	if len(content) > maxContentBytes {
		fmt.Fprintf(stderr, "ctn: %s is larger than %d bytes\n", file, maxContentBytes)
		return exitUsage
	}
	sum := sha256.Sum256(content)
	report.SHA256 = hex.EncodeToString(sum[:])

	store := adapter.NewStore(adapter.Home())
	chosen, prefix, err := chooseEngine(store, *engineName, file)
	if err != nil {
		fmt.Fprintf(stderr, "ctn: %v\n", err)
		return exitUsage
	}
	report.Engine, report.Selection = chosen.Manifest.Name, *selection
	if err := store.AssertTrusted(chosen); err != nil {
		return finish(exitUnverified, "UNSUPPORTED", fmt.Sprintf("engine %s is not trusted: run ctx adapter trust %s", chosen.Manifest.Name, chosen.Manifest.Name))
	}
	isolation, reason := engineIsolation(chosen, *selection)
	report.Isolation = isolation
	if isolation != "sandboxed" {
		return finish(exitUnverified, "UNSUPPORTED", reason)
	}

	// An outer limit beyond the engine's own, so an engine that ignores its
	// timeout cannot hang the verification.
	ctx, cancel := context.WithTimeout(context.Background(), *timeout+5*time.Second)
	defer cancel()
	arguments := append([]string{"--timeout", timeout.String(), prefix + file}, rest[1:]...)
	command, err := chosen.CommandContext(ctx, adapter.Invocation{Operation: "run", Selection: *selection, Arguments: arguments})
	if err != nil {
		return finish(exitUnverified, "UNSUPPORTED", err.Error())
	}
	var captured bytes.Buffer
	command.Stdout, command.Stderr = &captured, &captured
	started := time.Now()
	runErr := command.Run()
	report.Millis = time.Since(started).Milliseconds()
	code := 0
	if runErr != nil {
		var exit *exec.ExitError
		switch {
		case errors.Is(ctx.Err(), context.DeadlineExceeded):
			code = exitTimeout
		case errors.As(runErr, &exit):
			code = exit.ExitCode()
		default:
			return finish(exitUnverified, "UNSUPPORTED", runErr.Error())
		}
	}
	report.ExitCode = code
	detail := strings.TrimSpace(captured.String())
	if len(detail) > 400 {
		detail = detail[:400] + "…"
	}
	switch code {
	case 0:
		return finish(exitPass, "PASS", "")
	case exitTimeout:
		return finish(exitTimeout, "TIMEOUT", fmt.Sprintf("did not finish within %s", *timeout))
	case 126, 127:
		return finish(exitUnverified, "UNSUPPORTED", detail)
	default:
		return finish(exitFail, "FAIL", detail)
	}
}

func printVerdict(w io.Writer, v verdict, asJSON bool) {
	if asJSON {
		data, _ := json.Marshal(v)
		fmt.Fprintln(w, string(data))
		return
	}
	line := fmt.Sprintf("%s %s sha256:%s", v.Verdict, v.File, v.SHA256[:12])
	if v.Engine != "" {
		line += fmt.Sprintf(" engine=%s", v.Engine)
	}
	if v.Isolation != "" {
		line += fmt.Sprintf(" isolation=%s", v.Isolation)
	}
	if v.Millis > 0 {
		line += fmt.Sprintf(" %dms", v.Millis)
	}
	fmt.Fprintln(w, line)
	if v.Reason != "" {
		fmt.Fprintf(w, "  %s\n", v.Reason)
	}
}

// chooseEngine finds the engine adapter for the content: the named one, or the
// one whose kind the file's extension implies.
func chooseEngine(store *adapter.Store, name, file string) (*adapter.Adapter, string, error) {
	installed, err := store.List()
	if err != nil {
		return nil, "", err
	}
	entry, known := kinds[strings.ToLower(filepath.Ext(file))]
	if name == "" && !known {
		return nil, "", fmt.Errorf("cannot tell which engine runs %s; name one with --engine", file)
	}
	for _, candidate := range installed {
		isEngine, matches := false, false
		for _, support := range candidate.Manifest.Supports {
			if support == "engine" {
				isEngine = true
			}
			if name == "" && support == entry.kind {
				matches = true
			}
		}
		if !isEngine {
			continue
		}
		if name != "" && candidate.Manifest.Name == name {
			prefix := ""
			for _, k := range kinds {
				for _, support := range candidate.Manifest.Supports {
					if support == k.kind {
						prefix = k.prefix
					}
				}
			}
			return candidate, prefix, nil
		}
		if matches {
			return candidate, entry.prefix, nil
		}
	}
	if name != "" {
		return nil, "", fmt.Errorf("engine %s is not installed (see ctn adapter ls)", name)
	}
	return nil, "", fmt.Errorf("no engine adapter for %s content is installed", entry.kind)
}

// engineIsolation reads what the engine says it confines. With a selection it
// must be that context that is sandboxed; without one every context must be,
// because the adapter decides which one runs.
func engineIsolation(engine *adapter.Adapter, selection string) (string, string) {
	command, err := engine.Command(adapter.Invocation{Operation: "observe"})
	if err != nil {
		return "unknown", fmt.Sprintf("engine %s cannot report its isolation: %v", engine.Manifest.Name, err)
	}
	var out bytes.Buffer
	command.Stdout = &out
	if err := command.Run(); err != nil {
		return "unknown", fmt.Sprintf("engine %s could not report its isolation: %v", engine.Manifest.Name, err)
	}
	var observed struct {
		Contexts []struct {
			Selection  string            `json:"selection"`
			Attributes map[string]string `json:"attributes"`
		} `json:"contexts"`
	}
	if err := json.Unmarshal(out.Bytes(), &observed); err != nil || len(observed.Contexts) == 0 {
		return "unknown", fmt.Sprintf("engine %s reported no isolation, so its content is not run", engine.Manifest.Name)
	}
	for _, context := range observed.Contexts {
		if selection != "" && context.Selection != selection {
			continue
		}
		isolation := context.Attributes["isolation"]
		if isolation != "sandboxed" {
			if isolation == "" {
				isolation = "unreported"
			}
			which := context.Selection
			return isolation, fmt.Sprintf("engine %s (%s) has isolation %s, not sandboxed, so its content is not run", engine.Manifest.Name, which, isolation)
		}
	}
	if selection != "" {
		found := false
		for _, context := range observed.Contexts {
			found = found || context.Selection == selection
		}
		if !found {
			return "unknown", fmt.Sprintf("engine %s has no selection %q", engine.Manifest.Name, selection)
		}
	}
	return "sandboxed", ""
}
