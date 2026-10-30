package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/webong/ext/pkg/ext-go/engine/host/adapter"
	"github.com/webong/ext/ctn/internal/content"
)

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

	// Read the content and compute its digest.
	data, err := os.ReadFile(file)
	if err != nil {
		fmt.Fprintf(stderr, "ctn: %v\n", err)
		return exitUsage
	}
	if int64(len(data)) > maxContentBytes {
		fmt.Fprintf(stderr, "ctn: %s is larger than %d bytes\n", file, maxContentBytes)
		return exitUsage
	}
	sum := sha256.Sum256(data)
	sha256Hex := hex.EncodeToString(sum[:])

	// Choose the engine adapter.
	store := adapter.NewStore(adapter.Home())
	chosen, prefix, err := chooseEngine(store, *engineName, file)
	if err != nil {
		fmt.Fprintf(stderr, "ctn: %v\n", err)
		return exitUsage
	}

	// Build the content backend and policy from the engine adapter.
	backend := &engineBackend{adapter: chosen, prefix: prefix}
	policy := &enginePolicy{store: store}

	// Run verification through the reusable content module.
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	result, err := content.Verify(ctx, file, *selection, backend, policy, content.Limits{}, *timeout, nil)
	if err != nil {
		fmt.Fprintf(stderr, "ctn: %v\n", err)
		return exitUsage
	}

	// Map the content result to a ctn exit code and print the verdict.
	code := resultToExitCode(result)
	report := verdict{
		Verdict:   string(result.Verdict),
		File:      file,
		SHA256:    sha256Hex,
		Engine:    result.Backend,
		Selection: *selection,
		Isolation: result.Isolation,
		ExitCode:  result.ExitCode,
		Millis:    result.Millis,
		Reason:    result.Reason,
	}
	printVerdict(stdout, report, *asJSON)
	return code
}

func resultToExitCode(result *content.Result) int {
	switch result.Verdict {
	case content.VerdictPass:
		return exitPass
	case content.VerdictFail:
		return exitFail
	case content.VerdictTimeout:
		return exitTimeout
	case content.VerdictUnsupported:
		return exitUnverified
	default:
		return exitUnverified
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
