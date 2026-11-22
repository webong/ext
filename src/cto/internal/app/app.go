// Package app is the containment product's command. It is the launch step of
// docs/adr-sandboxing.md: a runner (the
// plugin engine, workload's local driver, a person) starts `ext sandbox exec` with a grant, and
// the command confines its own process with src/cto/internal/confine and becomes the guest.
//
//	cto exec --grant FILE [--driver NAME] [--accept CLASS,...] [--runner CLASS,...]
//	         [--status-fd N] [--dir DIR] -- PATH [ARG...]
//	cto exec --launch-fd N [--status-fd N]
//	cto check [--json]
//
// With --grant the grant is read from a JSON file (a src/cto/sandbox Policy) and the guest's base
// environment is the caller's PATH only; a grant with secrets needs --launch-fd. With
// --launch-fd the whole launch (confine.Launch, secrets' values included) is read from an
// inherited file descriptor, which is how a runner passes it without putting a secret on a
// command line. --status-fd writes the one status line (the report, or the code of a refusal)
// to an inherited descriptor before the guest starts.
package app

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/webong/ext/src/cto/internal/confine"
	"github.com/webong/ext/src/cto/sandbox"
)

// Version is the version of this build of cto.
var Version = "0.1.0-dev"

// The exit statuses of `sandbox exec` when it does not become the guest.
const (
	ExitUsage   = 2   // the command line is wrong
	ExitRefused = 125 // the launch was refused: the status line or stderr says why
	ExitExec    = 126 // the guest could not be started after the process was confined
)

// MaxLaunchBytes bounds a launch read from a descriptor.
const MaxLaunchBytes = 4 << 20

// Run runs the containment command. name is the command prefix used in messages,
// either "cto" or "ext sandbox".
func Run(name string, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintf(stderr, "usage: %s exec|check\n", name)
		return ExitUsage
	}
	switch args[0] {
	case "exec":
		return execCommand(name, args[1:], stderr)
	case "check":
		return checkCommand(name, args[1:], stdout, stderr)
	case "help", "-h", "--help":
		fmt.Fprintf(stdout, "usage: %s exec --grant FILE [flags] -- PATH [ARG...]\n       %s exec --launch-fd N [--status-fd N]\n       %s check [--json]\n", name, name, name)
		return 0
	}
	fmt.Fprintf(stderr, "%s: unknown command %q\n", name, args[0])
	return ExitUsage
}

func list(value string) []string {
	if value == "" {
		return nil
	}
	return strings.Split(value, ",")
}

func execCommand(name string, args []string, stderr io.Writer) int {
	flags := flag.NewFlagSet(name+" exec", flag.ContinueOnError)
	flags.SetOutput(stderr)
	grantFile := flags.String("grant", "", "a JSON file holding the grant (a src/cto/sandbox Policy)")
	launchFD := flags.Int("launch-fd", -1, "an inherited descriptor holding the whole launch as JSON")
	statusFD := flags.Int("status-fd", -1, "an inherited descriptor for the status line")
	driver := flags.String("driver", "", "the confinement driver; empty uses the only one available")
	accept := flags.String("accept", "", "classes to run without, comma-separated")
	runner := flags.String("runner", "", "classes the caller enforces itself, comma-separated")
	dir := flags.String("dir", "", "the guest's working directory")
	if err := flags.Parse(args); err != nil {
		return ExitUsage
	}
	var status io.Writer
	if *statusFD >= 0 {
		status = os.NewFile(uintptr(*statusFD), "status")
		if status == nil {
			fmt.Fprintf(stderr, "%s exec: no descriptor %d\n", name, *statusFD)
			return ExitUsage
		}
	}
	var launch confine.Launch
	switch {
	case *launchFD >= 0 && *grantFile == "" && flags.NArg() == 0:
		file := os.NewFile(uintptr(*launchFD), "launch")
		if file == nil {
			fmt.Fprintf(stderr, "%s exec: no descriptor %d\n", name, *launchFD)
			return ExitUsage
		}
		data, err := io.ReadAll(io.LimitReader(file, MaxLaunchBytes+1))
		file.Close()
		if err != nil || len(data) > MaxLaunchBytes || decode(data, &launch) != nil {
			fmt.Fprintf(stderr, "%s exec: the launch is not valid JSON within %d bytes\n", name, MaxLaunchBytes)
			return ExitUsage
		}
	case *grantFile != "" && *launchFD < 0 && flags.NArg() > 0:
		data, err := os.ReadFile(*grantFile)
		if err != nil {
			fmt.Fprintf(stderr, "%s exec: %v\n", name, err)
			return ExitUsage
		}
		if err := decode(data, &launch.Grant); err != nil {
			fmt.Fprintf(stderr, "%s exec: the grant is not valid JSON: %v\n", name, err)
			return ExitUsage
		}
		launch.Path, launch.Args = flags.Arg(0), flags.Args()[1:]
		launch.Driver, launch.Accept, launch.Runner, launch.Dir = *driver, list(*accept), list(*runner), *dir
		if path := os.Getenv("PATH"); path != "" {
			launch.Env = []string{"PATH=" + path}
		}
	default:
		fmt.Fprintf(stderr, "usage: %s exec --grant FILE [flags] -- PATH [ARG...] | --launch-fd N\n", name)
		return ExitUsage
	}
	code, err := confine.Exec(launch, status)
	if err == nil {
		return code // Windows: the guest ran and exited
	}
	fmt.Fprintf(stderr, "%s exec: %v\n", name, err)
	if errors.Is(err, confine.ErrDriver) {
		return ExitExec
	}
	return ExitRefused
}

func decode(data []byte, out any) error {
	d := json.NewDecoder(strings.NewReader(string(data)))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return err
	}
	if d.More() {
		return errors.New("trailing data")
	}
	return nil
}

func checkCommand(name string, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet(name+" check", flag.ContinueOnError)
	flags.SetOutput(stderr)
	asJSON := flags.Bool("json", false, "print the capabilities as JSON")
	if err := flags.Parse(args); err != nil {
		return ExitUsage
	}
	var all []sandbox.Capabilities
	for _, d := range confine.Drivers() {
		all = append(all, d.Capabilities())
	}
	if *asJSON {
		data, _ := json.Marshal(map[string]any{"drivers": all})
		fmt.Fprintln(stdout, string(data))
		return 0
	}
	if len(all) == 0 {
		fmt.Fprintln(stdout, "no confinement driver on this platform yet")
		return 0
	}
	for _, c := range all {
		var enforced, missing []string
		for class, s := range c.Classes {
			if s.Enforced {
				enforced = append(enforced, class)
			} else {
				missing = append(missing, class+" ("+s.Reason+")")
			}
		}
		sort.Strings(enforced)
		sort.Strings(missing)
		fmt.Fprintf(stdout, "%s (%s)\n  enforces: %s\n  does not: %s\n", c.Driver, c.Boundary, strings.Join(enforced, ", "), strings.Join(missing, ", "))
	}
	return 0
}
