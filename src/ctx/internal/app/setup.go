package app

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	modpkg "github.com/webong/ext/pkg/ext-go/engine/host/adapter"
	"github.com/webong/ext/pkg/ext-go/engine/host/adapter/adaptercmd"
)

func catalogStore() *modpkg.Store            { return adaptercmd.CatalogStore() }
func installedAdapterNames() map[string]bool { return adaptercmd.InstalledAdapterNames() }
func addCatalogAdapter(name string) (*modpkg.Adapter, error) {
	return adaptercmd.AddCatalogAdapter(name)
}
func refreshCatalogAdapters(w io.Writer) error { return adaptercmd.RefreshCatalogAdapters(w) }

func setupCommand(args []string, input io.Reader, stdout, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "adapters" {
		args = args[1:]
	}
	mode, selection := "interactive", ""
	for len(args) > 0 {
		switch {
		case args[0] == "--all" && mode == "interactive":
			mode, args = "all", args[1:]
		case args[0] == "--minimal" && mode == "interactive":
			mode, args = "minimal", args[1:]
		case args[0] == "--adapters" && mode == "interactive" && len(args) >= 2:
			mode, selection, args = "selected", args[1], args[2:]
		case strings.HasPrefix(args[0], "--adapters=") && mode == "interactive":
			mode, selection, args = "selected", strings.TrimPrefix(args[0], "--adapters="), args[1:]
		default:
			return setupUsage(stderr)
		}
	}

	if mode == "minimal" {
		fmt.Fprintln(stdout, "No new adapters selected. Existing adapters were preserved.")
		return 0
	}
	available, err := catalogStore().List()
	if err != nil {
		return reportError(stderr, err)
	}
	if len(available) == 0 {
		fmt.Fprintln(stderr, "ctx: adapter catalog is empty; rerun the installer with adapter selection or set CTX_CATALOG_HOME")
		return 1
	}
	if mode == "interactive" {
		selection, err = promptAdapterSelection(available, input, stdout)
		if err != nil {
			fmt.Fprintln(stderr, "ctx: no interactive input; use --adapters, --all, or --minimal")
			return 2
		}
		switch selection {
		case "", "none":
			fmt.Fprintln(stdout, "No adapters selected.")
			return 0
		case "all":
			mode = "all"
		}
	}

	names := catalogAdapterNames(available)
	if mode != "all" {
		names, err = parseAdapterSelection(selection, available)
		if err != nil {
			return reportErrorCode(stderr, err, 2)
		}
	}
	for _, name := range names {
		if _, err := addCatalogAdapter(name); err != nil {
			return reportError(stderr, err)
		}
		fmt.Fprintf(stdout, "installed adapter %s\n", name)
	}
	return 0
}

func setupUsage(stderr io.Writer) int {
	fmt.Fprintln(stderr, "ctx: setup accepts adapters, --all, --minimal, or --adapters <name,...>")
	return 2
}

func promptAdapterSelection(available []*modpkg.Adapter, input io.Reader, output io.Writer) (string, error) {
	installed := installedAdapterNames()
	fmt.Fprintln(output, "Available adapters:")
	for index, candidate := range available {
		state := ""
		if installed[candidate.Manifest.Name] {
			state = " [installed]"
		}
		fmt.Fprintf(output, "  %2d) %-12s %-12s %s%s\n", index+1, candidate.Manifest.Name, candidate.Manifest.Runtime, candidate.Manifest.Description, state)
	}
	fmt.Fprint(output, "Select names or numbers separated by commas (all/none): ")
	line, err := bufio.NewReader(input).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func parseAdapterSelection(value string, available []*modpkg.Adapter) ([]string, error) {
	byName := make(map[string]bool, len(available))
	for _, candidate := range available {
		byName[candidate.Manifest.Name] = true
	}
	seen := map[string]bool{}
	var names []string
	for _, token := range strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' }) {
		name := token
		if index, err := strconv.Atoi(token); err == nil {
			if index < 1 || index > len(available) {
				return nil, fmt.Errorf("adapter number %d is out of range", index)
			}
			name = available[index-1].Manifest.Name
		}
		if !byName[name] {
			return nil, fmt.Errorf("adapter %s is not in the installed catalog", name)
		}
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return nil, errors.New("no adapters selected")
	}
	return names, nil
}

func catalogAdapterNames(available []*modpkg.Adapter) []string {
	names := make([]string, 0, len(available))
	for _, candidate := range available {
		names = append(names, candidate.Manifest.Name)
	}
	return names
}
