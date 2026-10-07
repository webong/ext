package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/webong/ext/ctx/internal/config"
	"github.com/webong/ext/pkg/graph"
	"github.com/webong/ext/pkg/graph/system"
)

func graphCommand(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		args = []string{"status"}
	}
	system, err := systemgraph.Open(configHomePath())
	if err != nil {
		fmt.Fprintf(stderr, "ctx: open graph: %v\n", err)
		return 1
	}
	defer system.Close()
	resolver, resolveErr := newResolver()
	if resolveErr == nil {
		profile, _, _ := resolver.ActiveProfile()
		if observeErr := system.ObserveShell(context.Background(), currentDirectory(), profile, observedSelections(resolver)); observeErr != nil {
			fmt.Fprintf(stderr, "ctx: warning: could not observe shell for graph query: %v\n", observeErr)
		}
	}
	ctx := context.Background()
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	switch args[0] {
	case "processes", "process":
		return graphProcessCommand(ctx, system, args[0], args[1:], stdout, stderr)
	case "shells", "filesystems", "webviews":
		if len(args) != 1 {
			fmt.Fprintf(stderr, "ctx: graph %s takes no arguments\n", args[0])
			return 2
		}
		inventory := systemgraph.HostInventory{}
		var result any
		switch args[0] {
		case "shells":
			inventory.Shells, err = systemgraph.DiscoverShells(ctx)
			result = inventory.Shells
		case "filesystems":
			inventory.Filesystems, err = systemgraph.DiscoverFilesystems(ctx)
			result = inventory.Filesystems
		case "webviews":
			inventory.Webviews, err = systemgraph.DiscoverWebviews(ctx)
			result = inventory.Webviews
		}
		if err != nil {
			return reportError(stderr, err)
		}
		if err := system.ObserveHost(ctx, inventory); err != nil {
			return reportError(stderr, err)
		}
		if err := encoder.Encode(result); err != nil {
			return reportError(stderr, err)
		}
		return 0
	case "scan":
		if len(args) != 1 {
			fmt.Fprintln(stderr, "ctx: graph scan takes no arguments")
			return 2
		}
		host, err := system.ScanHost(ctx)
		if err != nil {
			return reportError(stderr, err)
		}
		processes, err := system.ScanProcesses(ctx, systemgraph.ProcessOptions{})
		if err != nil {
			return reportError(stderr, err)
		}
		if resolveErr != nil {
			return reportError(stderr, resolveErr)
		}
		adapters, err := scanMachineInventory(resolver, system)
		if err != nil {
			return reportError(stderr, err)
		}
		count := 0
		for _, adapter := range adapters {
			count += len(adapter.Contexts)
			if adapter.DiscoveryStatus != "ok" && adapter.DiscoveryStatus != "not-supported" && adapter.DiscoveryStatus != "untrusted" {
				fmt.Fprintf(stderr, "ctx: discovery unavailable for %s (%s)\n", adapter.Name, adapter.DiscoveryStatus)
			}
		}
		fmt.Fprintf(stdout, "observed %d adapters and %d contexts\n", len(adapters), count)
		fmt.Fprintf(stdout, "observed %d shells and %d mounted filesystems\n", len(host.Shells), len(host.Filesystems))
		fmt.Fprintf(stdout, "observed %d shared webview runtimes\n", len(host.Webviews))
		fmt.Fprintf(stdout, "observed %d processes (%s)\n", len(processes.Processes), processes.Enumeration.State)
		return 0
	case "resolve":
		if len(args) > 1 && (args[1] == "shell" || args[1] == "filesystem" || args[1] == "webview") {
			return graphHostResolve(system, args[1], args[2:], stdout, stderr)
		}
		if resolveErr != nil {
			return reportError(stderr, resolveErr)
		}
		runtimeName := ""
		capabilities := []string(nil)
		supports := []string(nil)
		shareReady := []string(nil)
		if len(args) > 1 {
			runtimeName = args[1]
			if runtimeName == "all" {
				runtimeName = ""
			}
			for index := 2; index < len(args); index++ {
				if args[index] == "--supports" {
					if index+1 >= len(args) || args[index+1] == "" || strings.HasPrefix(args[index+1], "--") {
						fmt.Fprintln(stderr, "ctx: graph resolve --supports needs a resource kind")
						return 2
					}
					index++
					supports = append(supports, args[index])
					continue
				}
				if args[index] == "--share" {
					if index+1 >= len(args) || args[index+1] == "" || strings.HasPrefix(args[index+1], "--") {
						fmt.Fprintln(stderr, "ctx: graph resolve --share needs a browser resource.operation")
						return 2
					}
					index++
					shareReady = append(shareReady, args[index])
					continue
				}
				capabilities = append(capabilities, args[index])
			}
		}
		if _, err := scanMachineInventory(resolver, system); err != nil {
			return reportError(stderr, err)
		}
		inventory, err := system.ResolveInventory(ctx, runtimeName)
		if err != nil {
			return reportError(stderr, err)
		}
		selected := inventory.Contexts[:0]
		for _, candidate := range inventory.Contexts {
			if candidateOffers(candidate, capabilities...) && candidateSupports(candidate, supports...) && candidateShareReady(candidate, shareReady...) {
				selected = append(selected, candidate)
			}
		}
		inventory.Contexts = selected
		if err := encoder.Encode(inventory); err != nil {
			return reportError(stderr, err)
		}
		return 0
	case "status":
		if len(args) != 1 {
			fmt.Fprintln(stderr, "ctx: graph status takes no arguments")
			return 2
		}
		snapshot, err := system.Store.Snapshot(ctx, systemgraph.Namespace)
		if err != nil {
			fmt.Fprintf(stderr, "ctx: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "store: %s/graph.json\nnamespace: %s\nrevision: %d\ncursor: %d\nvertices: %d\nedges: %d\n", configHomePath(), snapshot.Namespace, snapshot.Revision, snapshot.Cursor, len(snapshot.Vertices), len(snapshot.Edges))
		inventory, err := system.ResolveInventory(ctx, "")
		if err != nil {
			return reportError(stderr, err)
		}
		if !inventory.ObservedAt.IsZero() {
			fmt.Fprintf(stdout, "last_scan: %s\nobserved_contexts: %d\n", inventory.ObservedAt.Format(time.RFC3339), len(inventory.Contexts))
		}
		return 0
	case "vertices":
		kind := ""
		if len(args) > 2 {
			fmt.Fprintln(stderr, "ctx: graph vertices accepts at most one kind")
			return 2
		}
		if len(args) == 2 {
			kind = args[1]
			if kind != "" && !strings.HasPrefix(kind, systemgraph.Namespace+"/") {
				kind = systemgraph.Namespace + "/" + kind
			}
		}
		values, err := system.Store.QueryVertices(ctx, graph.VertexQuery{Namespace: systemgraph.Namespace, Kind: kind, Limit: 1000})
		if err != nil {
			fmt.Fprintf(stderr, "ctx: %v\n", err)
			return 1
		}
		if err = encoder.Encode(values); err != nil {
			fmt.Fprintf(stderr, "ctx: %v\n", err)
			return 1
		}
		return 0
	case "edges":
		relationship := ""
		if len(args) > 2 {
			fmt.Fprintln(stderr, "ctx: graph edges accepts at most one relationship")
			return 2
		}
		if len(args) == 2 {
			relationship = args[1]
			if relationship != "" && !strings.HasPrefix(relationship, systemgraph.Namespace+"/") {
				relationship = systemgraph.Namespace + "/" + relationship
			}
		}
		values, err := system.Store.QueryEdges(ctx, graph.EdgeQuery{Namespace: systemgraph.Namespace, Type: relationship, Limit: 1000})
		if err != nil {
			fmt.Fprintf(stderr, "ctx: %v\n", err)
			return 1
		}
		if err = encoder.Encode(values); err != nil {
			fmt.Fprintf(stderr, "ctx: %v\n", err)
			return 1
		}
		return 0
	case "snapshot":
		if len(args) != 1 {
			fmt.Fprintln(stderr, "ctx: graph snapshot takes no arguments")
			return 2
		}
		snapshot, err := system.Store.Snapshot(ctx, systemgraph.Namespace)
		if err != nil {
			fmt.Fprintf(stderr, "ctx: %v\n", err)
			return 1
		}
		if err = encoder.Encode(snapshot); err != nil {
			fmt.Fprintf(stderr, "ctx: %v\n", err)
			return 1
		}
		return 0
	case "changes":
		if len(args) > 2 {
			fmt.Fprintln(stderr, "ctx: graph changes accepts at most one cursor")
			return 2
		}
		var cursor uint64
		if len(args) == 2 {
			cursor, err = strconv.ParseUint(args[1], 10, 64)
			if err != nil {
				fmt.Fprintln(stderr, "ctx: graph changes cursor must be an unsigned integer")
				return 2
			}
		}
		changes, err := system.Store.Changes(ctx, systemgraph.Namespace, cursor, 1000)
		if err != nil {
			fmt.Fprintf(stderr, "ctx: %v\n", err)
			return 1
		}
		if err = encoder.Encode(changes); err != nil {
			fmt.Fprintf(stderr, "ctx: %v\n", err)
			return 1
		}
		return 0
	default:
		fmt.Fprintf(stderr, "ctx: unknown graph command %s\n", args[0])
		fmt.Fprintln(stderr, "ctx: use graph scan, shells, filesystems, webviews, processes, process <pid>, resolve [runtime|all] [capability...] [--supports kind] [--share resource.operation], status, vertices, edges, snapshot, or changes [cursor]")
		return 2
	}
}

func candidateOffers(candidate systemgraph.ContextCandidate, required ...string) bool {
	for _, capability := range required {
		found := false
		for _, offered := range candidate.Capabilities {
			if capability == offered {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func candidateSupports(candidate systemgraph.ContextCandidate, required ...string) bool {
	for _, kind := range required {
		found := false
		for _, supported := range candidate.Supports {
			if kind == supported {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func candidateShareReady(candidate systemgraph.ContextCandidate, required ...string) bool {
	for _, operation := range required {
		if candidate.BrowserShare[operation] != "ready" {
			return false
		}
	}
	return true
}

func recordSystemContext(resolver *config.Resolver, stderr io.Writer) {
	system, err := systemgraph.Open(configHomePath())
	if err != nil {
		fmt.Fprintf(stderr, "ctx: warning: could not record system context: %v\n", err)
		return
	}
	defer system.Close()
	profile, _, _ := resolver.ActiveProfile()
	cwd := currentDirectory()
	selections := observedSelections(resolver)
	if err := system.ObserveShell(context.Background(), cwd, profile, selections); err != nil {
		fmt.Fprintf(stderr, "ctx: warning: could not record system context: %v\n", err)
	}
}

func observedSelections(resolver *config.Resolver) map[string]string {
	selections := map[string]string{}
	if installed, err := adapterStore().List(); err == nil {
		for _, candidate := range installed {
			if !candidate.IsSelectable() {
				continue
			}
			if resolved, resolveErr := resolver.Resolve(candidate.Manifest.SelectorKey); resolveErr == nil && resolved.Value != "" {
				selections[candidate.Manifest.SelectorKey] = resolved.Value
			}
		}
	}
	return selections
}

func recordWebContext(provider, profile string, args []string, stderr io.Writer) {
	system, err := systemgraph.Open(configHomePath())
	if err != nil {
		fmt.Fprintf(stderr, "ctx: warning: could not record web context: %v\n", err)
		return
	}
	defer system.Close()
	for _, arg := range args {
		scheme, host, ok := systemgraph.SafeOrigin(arg)
		if !ok {
			continue
		}
		if err := system.ObserveWeb(context.Background(), provider, profile, scheme, host); err != nil {
			fmt.Fprintf(stderr, "ctx: warning: could not record web context: %v\n", err)
			return
		}
	}
}
