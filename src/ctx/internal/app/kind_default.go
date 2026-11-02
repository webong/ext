package app

import (
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/webong/ext/ctx/internal/config"
	systemgraph "github.com/webong/ext/res/graph"
)

// A kind (php, jvm) can be supported by several adapters. Which one a caller
// should use is a preference the user states, per project in .ctx or for the whole
// user in config.toml, and resolve reports it; nothing is chosen when none is set.

var kindName = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// kindDefaultCommand sets, clears and lists the preferred adapter for a kind:
// ctx default <kind> <adapter> [--global], ctx default <kind> --clear [--global],
// ctx default ls.
func kindDefaultCommand(resolver *config.Resolver, args []string, stdout, stderr io.Writer) int {
	usage := func() int {
		fmt.Fprintln(stderr, "ctx: default needs <kind> <adapter> [--global], <kind> --clear [--global] or ls")
		return 2
	}
	if len(args) == 0 {
		return usage()
	}
	if args[0] == "ls" {
		if len(args) != 1 {
			return usage()
		}
		return listKindDefaults(resolver, stdout, stderr)
	}
	kind := args[0]
	if !kindName.MatchString(kind) {
		fmt.Fprintf(stderr, "ctx: %q is not a kind\n", kind)
		return 2
	}
	global, clear := false, false
	var names []string
	for _, arg := range args[1:] {
		switch arg {
		case "--global":
			global = true
		case "--clear":
			clear = true
		default:
			names = append(names, arg)
		}
	}
	projectFile := filepath.Join(currentDirectory(), ".ctx")
	key, path := config.KindKey(kind), projectFile
	if global {
		key, path = config.KindKey(kind)+"_default", resolver.ConfigPath
	}
	if clear {
		if len(names) != 0 {
			return usage()
		}
		if err := config.RemoveFlat(path, key); err != nil {
			return reportError(stderr, err)
		}
		fmt.Fprintf(stdout, "%s: default cleared in %s\n", kind, path)
		return 0
	}
	if len(names) != 1 {
		return usage()
	}
	candidate, err := adapterStore().Load(names[0])
	if err != nil || candidate.Declarative() {
		fmt.Fprintf(stderr, "ctx: adapter %s is not installed\n", names[0])
		return 1
	}
	if !candidate.HasSupport(kind) {
		fmt.Fprintf(stderr, "ctx: adapter %s does not support %s\n", names[0], kind)
		return 1
	}
	var writeErr error
	if global {
		writeErr = config.SetRoot(path, key, names[0])
	} else {
		writeErr = config.SetFlat(path, key, names[0])
		if writeErr == nil {
			ensureGitExclude(path)
		}
	}
	if writeErr != nil {
		return reportError(stderr, writeErr)
	}
	fmt.Fprintf(stdout, "%s: %s in %s\n", kind, names[0], path)
	return 0
}

// listKindDefaults prints each kind that has a preference or more than one adapter
// offering it, so a missing preference is as visible as a set one.
func listKindDefaults(resolver *config.Resolver, stdout, stderr io.Writer) int {
	adapters, err := adapterStore().List()
	if err != nil {
		return reportError(stderr, err)
	}
	offered := map[string][]string{}
	for _, adapter := range adapters {
		for _, kind := range adapter.Manifest.Supports {
			offered[kind] = append(offered[kind], adapter.Manifest.Name)
		}
	}
	kinds := make([]string, 0, len(offered))
	for kind := range offered {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	for _, kind := range kinds {
		resolved, err := resolver.ResolveKind(kind)
		if err != nil {
			return reportError(stderr, err)
		}
		sort.Strings(offered[kind])
		switch {
		case resolved.Value != "":
			fmt.Fprintf(stdout, "%s\t%s\t%s\n", kind, resolved.Value, resolved.Source)
		case len(offered[kind]) > 1:
			fmt.Fprintf(stdout, "%s\t-\tnone set; offered by %s\n", kind, strings.Join(offered[kind], ", "))
		}
	}
	return 0
}

// markKindDefaults flags the candidates that belong to the preferred adapter of
// every kind the resolve asked for. It removes nothing.
func markKindDefaults(resolver *config.Resolver, kinds []string, contexts []systemgraph.ContextCandidate) error {
	if len(kinds) == 0 {
		return nil
	}
	preferred := make([]string, len(kinds))
	for i, kind := range kinds {
		resolved, err := resolver.ResolveKind(kind)
		if err != nil {
			return err
		}
		if resolved.Value == "" {
			return nil
		}
		preferred[i] = resolved.Value
	}
	for i := range contexts {
		contexts[i].Default = true
		for _, name := range preferred {
			contexts[i].Default = contexts[i].Default && contexts[i].Adapter == name
		}
	}
	return nil
}

// pickAdapter narrows the candidates of one kind to the contexts of a single
// adapter: the preferred one, or the only one there is. It never chooses between
// several adapters on its own, and it does not fall back from a preference that
// names an adapter that is gone.
func pickAdapter(resolver *config.Resolver, kinds []string, contexts []systemgraph.ContextCandidate) ([]systemgraph.ContextCandidate, error) {
	if len(kinds) != 1 {
		return nil, fmt.Errorf("graph resolve --pick needs exactly one --supports kind")
	}
	kind := kinds[0]
	seen := map[string]bool{}
	var adapters []string
	for _, candidate := range contexts {
		if !seen[candidate.Adapter] {
			seen[candidate.Adapter] = true
			adapters = append(adapters, candidate.Adapter)
		}
	}
	sort.Strings(adapters)
	resolved, err := resolver.ResolveKind(kind)
	if err != nil {
		return nil, err
	}
	chosen := resolved.Value
	switch {
	case chosen != "" && !seen[chosen]:
		return nil, fmt.Errorf("the default for %s is %s (%s), which is not among the adapters that offer it: %s", kind, chosen, resolved.Source, strings.Join(adapters, ", "))
	case chosen == "" && len(adapters) == 0:
		return nil, fmt.Errorf("no adapter supports %s", kind)
	case chosen == "" && len(adapters) > 1:
		return nil, fmt.Errorf("%d adapters support %s (%s); set one with ctx default %s <adapter>", len(adapters), kind, strings.Join(adapters, ", "), kind)
	case chosen == "":
		chosen = adapters[0]
	}
	picked := contexts[:0]
	for _, candidate := range contexts {
		if candidate.Adapter == chosen {
			picked = append(picked, candidate)
		}
	}
	return picked, nil
}
