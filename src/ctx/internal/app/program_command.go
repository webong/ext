package app

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/webong/ext/ctx/internal/config"
)

// programCommand records what the user knows about where a program came from.
// Adapters only suggest; recognise turns a suggestion into a recognition that
// discovery applies, and forget takes it back.
func programCommand(resolver *config.Resolver, args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "installer" {
		return installerCommand(args[1:], stdout, stderr)
	}
	if len(args) < 2 {
		fmt.Fprintln(stderr, "ctx: program needs recognise <path> [installer], forget <path> [installer] or installer <add|remove|ls>")
		return 2
	}
	action, rawPath, rest := args[0], args[1], args[2:]
	if (action != "recognise" && action != "forget") || len(rest) > 1 {
		fmt.Fprintln(stderr, "ctx: program needs recognise <path> [installer], forget <path> [installer] or installer <add|remove|ls>")
		return 2
	}
	installer := ""
	if len(rest) == 1 {
		installer = rest[0]
		if !managerInstanceName.MatchString(installer) {
			fmt.Fprintf(stderr, "ctx: %q is not an installer name\n", installer)
			return 2
		}
	}
	path, err := filepath.Abs(rawPath)
	if err != nil {
		return reportError(stderr, err)
	}
	real, err := filepath.EvalSymlinks(path)
	if action == "forget" && err != nil {
		real = path
	} else if err != nil {
		return reportError(stderr, fmt.Errorf("%s: %w", rawPath, err))
	}
	file, err := loadRecognitions()
	if err != nil {
		return reportError(stderr, err)
	}
	if action == "forget" {
		var kept []recognition
		removed := 0
		for _, item := range file.Recognised {
			if item.RealPath == real && (installer == "" || item.Installer == installer) {
				removed++
				continue
			}
			kept = append(kept, item)
		}
		if removed == 0 {
			fmt.Fprintf(stderr, "ctx: nothing recognised for %s\n", real)
			return 1
		}
		file.Recognised = kept
		if err := saveRecognitions(file); err != nil {
			return reportError(stderr, err)
		}
		fmt.Fprintf(stdout, "forgot %d recognition(s) for %s\n", removed, real)
		return 0
	}
	if info, err := os.Stat(real); err != nil || !info.Mode().IsRegular() {
		return reportError(stderr, fmt.Errorf("%s is not a file", real))
	}
	if installer == "" {
		suggested, err := suggestedInstallers(resolver, real)
		if err != nil {
			return reportError(stderr, err)
		}
		switch len(suggested) {
		case 0:
			fmt.Fprintf(stderr, "ctx: no adapter suggested an installer for %s; name one: ctx program recognise %s <installer>\n", real, rawPath)
			return 1
		case 1:
			installer = suggested[0]
		default:
			fmt.Fprintf(stderr, "ctx: more than one installer was suggested for %s (%s); name one\n", real, strings.Join(suggested, ", "))
			return 1
		}
	}
	for _, item := range file.Recognised {
		if item.RealPath == real && item.Installer == installer {
			fmt.Fprintf(stdout, "%s is already recognised as installed by %s\n", real, installer)
			return 0
		}
	}
	file.Recognised = append(file.Recognised, recognition{RealPath: real, Installer: installer})
	if err := saveRecognitions(file); err != nil {
		return reportError(stderr, err)
	}
	fmt.Fprintf(stdout, "recognised %s as installed by %s\n", real, installer)
	return 0
}

// suggestedInstallers returns what adapters suggested for a real file, from the
// machine graph.
func suggestedInstallers(resolver *config.Resolver, real string) ([]string, error) {
	inventory, err := resolvedMachineInventory(resolver)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var suggested []string
	for _, candidate := range inventory.Contexts {
		if candidate.Program == nil || candidate.Program.RealPath != real {
			continue
		}
		for _, name := range candidate.Program.Suggested {
			if !seen[name] {
				seen[name] = true
				suggested = append(suggested, name)
			}
		}
	}
	return suggested, nil
}
