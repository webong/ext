package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/webong/ext/ctx/internal/config"
	"github.com/webong/ext/pkg/graph/system"
	modpkg "github.com/webong/ext/pkg/plugin/adapter"
	browsershare "github.com/webong/ext/res/browser/contract"
)

// scanMachineInventory asks installed adapters for their declared capabilities
// and, where list has a line-oriented contract, configured contexts. The graph
// is an observation for discovery, never authority to execute an adapter.
func scanMachineInventory(resolver *config.Resolver, system *systemgraph.Graph) ([]systemgraph.AdapterObservation, error) {
	store := adapterStore()
	installed, err := store.List()
	if err != nil {
		return nil, err
	}
	observations := make([]systemgraph.AdapterObservation, len(installed))
	errors := make([]error, len(installed))
	var workers sync.WaitGroup
	limit := make(chan struct{}, 4)
	for index, candidate := range installed {
		workers.Add(1)
		limit <- struct{}{}
		go func(index int, candidate *modpkg.Adapter) {
			defer workers.Done()
			defer func() { <-limit }()
			observations[index], errors[index] = observeAdapterCandidate(resolver, store, candidate)
		}(index, candidate)
	}
	workers.Wait()
	for _, err := range errors {
		if err != nil {
			return nil, err
		}
	}
	if system == nil {
		return observations, nil
	}
	registry, err := readManagerRegistry()
	if err != nil {
		return observations, err
	}
	aliases := make([]systemgraph.AliasObservation, 0, len(registry.Instances))
	for _, instance := range registry.Instances {
		metadata := map[string]string{}
		if instance.Virtualizer != "" {
			metadata["virtualizer"] = instance.Virtualizer
		}
		if instance.Machine != "" {
			metadata["machine"] = instance.Machine
		}
		aliases = append(aliases, systemgraph.AliasObservation{
			Space: "manager", Name: instance.Name, Adapter: instance.Provider,
			Selection: instance.Selection, Metadata: metadata,
		})
	}
	return observations, system.ObserveInventory(context.Background(), observations, aliases)
}

func observeAdapterCandidate(resolver *config.Resolver, store *modpkg.Store, candidate *modpkg.Adapter) (systemgraph.AdapterObservation, error) {
	trusted, err := store.IsTrusted(candidate)
	if err != nil {
		return systemgraph.AdapterObservation{}, err
	}
	observation := systemgraph.AdapterObservation{
		Name: candidate.Manifest.Name, Runtime: candidate.Manifest.Runtime,
		Selector:         candidate.Manifest.SelectorKey,
		Surfaces:         append([]string(nil), candidate.Manifest.Surfaces...),
		Capabilities:     append([]string(nil), candidate.Manifest.Capabilities...),
		Supports:         append([]string(nil), candidate.Manifest.Supports...),
		BrowserShare:     append([]string(nil), candidate.Manifest.BrowserShare...),
		Trusted:          trusted,
		ListSupported:    candidate.HasCapability("list"),
		ObserveSupported: candidate.HasCapability("observe"),
		DiscoveryStatus:  "not-supported",
	}
	if !trusted {
		observation.DiscoveryStatus = "untrusted"
	}
	if trusted && candidate.HasCapability("observe") {
		var output boundedObservationBuffer
		var adapterError boundedObservationBuffer
		if code := invokeAdapter(resolver, candidate, "observe", "", nil, "", &output, &adapterError); code == 0 {
			contexts, resources, relations, parseErr := parseAdapterObservation(output.Bytes(), candidate.Manifest.Capabilities, candidate.Manifest.Supports, candidate.Manifest.BrowserShare)
			if parseErr != nil {
				observation.DiscoveryStatus = "invalid-response"
			} else {
				observation.Listed = true
				observation.DiscoveryStatus = "ok"
				observation.Contexts = contexts
				observation.Resources = resources
				observation.Relations = relations
			}
		} else {
			observation.DiscoveryStatus = fmt.Sprintf("exit:%d", code)
		}
	} else if trusted && candidate.HasCapability("list") {
		var output boundedObservationBuffer
		var adapterError boundedObservationBuffer
		if code := invokeAdapter(resolver, candidate, "list", "", nil, "", &output, &adapterError); code == 0 {
			observation.Listed = true
			observation.DiscoveryStatus = "ok"
			if candidate.IsRuntime("browser") || candidate.IsRuntime("manager") {
				seen := map[string]bool{}
				for _, line := range strings.Split(output.String(), "\n") {
					selection := strings.TrimSuffix(line, "\r")
					if candidate.IsRuntime("browser") {
						selection = strings.TrimPrefix(selection, candidate.Manifest.Name+":")
					}
					if selection != "" && !seen[selection] {
						observation.Contexts = append(observation.Contexts, systemgraph.ContextObservation{Selection: selection})
						seen[selection] = true
					}
				}
			}
		} else {
			observation.DiscoveryStatus = fmt.Sprintf("exit:%d", code)
		}
	}
	if trusted && observation.Listed && candidate.IsRuntime("browser") && len(candidate.Manifest.BrowserShare) > 0 {
		for index := range observation.Contexts {
			if index >= 32 {
				break
			}
			if observation.Contexts[index].BrowserShare == nil {
				observation.Contexts[index].BrowserShare = probeBrowserShare(resolver, candidate, observation.Contexts[index].Selection)
			}
		}
	}
	return observation, nil
}

// A browser probe reports prerequisites only. It must not read cookie values,
// unlock an OS key, or include profile paths in the graph.
func probeBrowserShare(resolver *config.Resolver, candidate *modpkg.Adapter, selection string) map[string]string {
	statuses := make(map[string]string, len(candidate.Manifest.BrowserShare))
	for _, operation := range candidate.Manifest.BrowserShare {
		statuses[operation] = "unknown"
	}
	var output boundedObservationBuffer
	var adapterError boundedObservationBuffer
	if code := invokeAdapter(resolver, candidate, "share", selection, []string{"status", "probe"}, "", &output, &adapterError); code != 0 {
		return statuses
	}
	var report browsershare.AvailabilityReport
	decoder := json.NewDecoder(bytes.NewReader(output.Bytes()))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&report); err != nil || report.Version != browsershare.AvailabilityVersion {
		return statuses
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return statuses
	}
	for operation, state := range report.Operations {
		if _, declared := statuses[operation]; declared && validBrowserShareState(state) {
			statuses[operation] = state
		}
	}
	return statuses
}

func scanMachineInventoryInGraph(resolver *config.Resolver) ([]systemgraph.AdapterObservation, error) {
	system, err := systemgraph.Open(configHomePath())
	if err != nil {
		return nil, err
	}
	defer system.Close()
	return scanMachineInventory(resolver, system)
}

func resolvedMachineInventory(resolver *config.Resolver) (systemgraph.Inventory, error) {
	system, err := systemgraph.Open(configHomePath())
	if err != nil {
		return systemgraph.Inventory{}, err
	}
	defer system.Close()
	if _, err := scanMachineInventory(resolver, system); err != nil {
		return systemgraph.Inventory{}, err
	}
	return system.ResolveInventory(context.Background(), "")
}

func freshMachineInventory(resolver *config.Resolver) (systemgraph.Inventory, error) {
	system, err := systemgraph.Open(configHomePath())
	if err != nil {
		return systemgraph.Inventory{}, err
	}
	defer system.Close()
	inventory, err := system.ResolveInventory(context.Background(), "")
	if err != nil {
		return systemgraph.Inventory{}, err
	}
	if inventory.FreshWithin(5 * time.Second) {
		return inventory, nil
	}
	if _, err := scanMachineInventory(resolver, system); err != nil {
		return systemgraph.Inventory{}, err
	}
	return system.ResolveInventory(context.Background(), "")
}

func printDiscoveredManagers(inventory systemgraph.Inventory, stdout io.Writer) int {
	count := 0
	for _, candidate := range inventory.Contexts {
		if candidate.Runtime != "manager" || candidate.Selection == "" {
			continue
		}
		fmt.Fprintf(stdout, "%s:%s\n", candidate.Adapter, candidate.Selection)
		count++
	}
	return count
}

type boundedObservationBuffer struct{ bytes.Buffer }

func (buffer *boundedObservationBuffer) Write(data []byte) (int, error) {
	if buffer.Len()+len(data) > maxAdapterObservationBytes {
		return 0, fmt.Errorf("adapter observation exceeds 1 MiB")
	}
	return buffer.Buffer.Write(data)
}
