// Package browserhost connects the portable browser resource to CTX's
// installed and trusted adapter store.
package browserhost

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	modpkg "github.com/webong/ext/pkg/plugin/adapter"
	"github.com/webong/ext/res/browser"
)

type Provider struct{ AdapterHome string }

func (provider Provider) Sources(ctx context.Context, options browser.Options) ([]browser.Source, []string, error) {
	store := modpkg.NewStore(adapterHome(provider.AdapterHome))
	selected, warnings, err := selectSources(ctx, store, options)
	if err != nil {
		return nil, warnings, err
	}
	sources := make([]browser.Source, 0, len(selected))
	for _, candidate := range selected {
		adapter, profile := candidate.adapter, candidate.profile
		sources = append(sources, browser.Source{
			Adapter: adapter.Manifest.Name, Profile: profile, Label: candidate.label,
			SupportsQuery: adapter.HasBrowserShare("cookie.query"),
			Invoke: func(ctx context.Context, operation string, args []string, input []byte) ([]byte, error) {
				return invoke(ctx, adapter, operation, profile, args, input)
			},
		})
	}
	return sources, warnings, nil
}

func (provider Provider) Normalize(ctx context.Context, name, profile string, request []byte) ([]byte, error) {
	store := modpkg.NewStore(adapterHome(provider.AdapterHome))
	adapter, err := store.Load(name)
	if err != nil {
		return nil, err
	}
	if !adapter.IsRuntime("browser") || !adapter.HasBrowserShare("cookie.normalize") || !adapter.HasCapability("share") {
		return nil, errors.New("selected adapter does not support cookie.normalize")
	}
	if err := store.AssertTrusted(adapter); err != nil {
		return nil, err
	}
	return invoke(ctx, adapter, "share", profile, []string{"cookie", "normalize"}, request)
}

func adapterHome(override string) string {
	if override != "" {
		return override
	}
	return modpkg.Home()
}

type selectedSource struct {
	adapter *modpkg.Adapter
	profile string
	label   string
}

func selectSources(ctx context.Context, store *modpkg.Store, options browser.Options) ([]selectedSource, []string, error) {
	if len(options.Sources) > 0 {
		sources := make([]selectedSource, 0, len(options.Sources))
		for _, endpoint := range options.Sources {
			name, profile, ok := strings.Cut(endpoint, ":")
			if !ok || name == "" || profile == "" || strings.ContainsAny(profile, "\r\n") {
				return nil, nil, fmt.Errorf("invalid browser endpoint %q", endpoint)
			}
			adapter, err := store.Load(name)
			if err != nil {
				return nil, nil, err
			}
			if err := checkSource(store, adapter); err != nil {
				return nil, nil, err
			}
			sources = append(sources, selectedSource{adapter, profile, endpoint})
		}
		return sources, nil, nil
	}
	var adapters []*modpkg.Adapter
	if len(options.Browsers) > 0 {
		for _, name := range options.Browsers {
			adapter, err := store.Load(name)
			if err != nil {
				return nil, nil, err
			}
			if err := checkSource(store, adapter); err != nil {
				return nil, nil, err
			}
			adapters = append(adapters, adapter)
		}
	} else {
		installed, err := store.List()
		if err != nil {
			return nil, nil, err
		}
		for _, adapter := range installed {
			if adapter.Manifest.BrowserQueryAuto && supportsCookieQuery(adapter) {
				if trusted, _ := store.IsTrusted(adapter); trusted {
					adapters = append(adapters, adapter)
				}
			}
		}
		sort.Slice(adapters, func(i, j int) bool {
			left, right := adapters[i].Manifest, adapters[j].Manifest
			if left.BrowserQueryPriority != right.BrowserQueryPriority {
				return left.BrowserQueryPriority < right.BrowserQueryPriority
			}
			return left.Name < right.Name
		})
	}
	var sources []selectedSource
	var warnings []string
	for _, adapter := range adapters {
		if err := ctx.Err(); err != nil {
			return nil, warnings, err
		}
		name := adapter.Manifest.Name
		if profile := options.Profiles[name]; profile != "" {
			if strings.ContainsAny(profile, "\r\n") {
				return nil, nil, fmt.Errorf("invalid profile for %s", name)
			}
			sources = append(sources, selectedSource{adapter, profile, name + ":" + profile})
			continue
		}
		if !adapter.HasCapability("list") {
			warnings = append(warnings, name+": profile discovery unavailable")
			continue
		}
		output, err := invoke(ctx, adapter, "list", "", nil, nil)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: %v", name, err))
			continue
		}
		for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
			profile, ok := strings.CutPrefix(strings.TrimSuffix(line, "\r"), name+":")
			if ok && profile != "" && !strings.ContainsAny(profile, "\r\n") {
				sources = append(sources, selectedSource{adapter, profile, name + ":" + profile})
			}
		}
	}
	if len(options.Browsers) == 0 && options.PreferredSource != "" {
		preferred, profile, ok := strings.Cut(options.PreferredSource, ":")
		if !ok || preferred == "" || profile == "" || strings.ContainsAny(profile, "\r\n") {
			return nil, nil, fmt.Errorf("invalid preferred browser endpoint %q", options.PreferredSource)
		}
		for index, source := range sources {
			if source.label == options.PreferredSource {
				ordered := make([]selectedSource, 0, len(sources))
				ordered = append(ordered, source)
				ordered = append(ordered, sources[:index]...)
				ordered = append(ordered, sources[index+1:]...)
				return ordered, warnings, nil
			}
		}
		adapter, err := store.Load(preferred)
		if err == nil {
			err = checkSource(store, adapter)
		}
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("preferred browser %s: %v", preferred, err))
		} else {
			sources = append([]selectedSource{{adapter: adapter, profile: profile, label: options.PreferredSource}}, sources...)
		}
	}
	return sources, warnings, nil
}

func checkSource(store *modpkg.Store, adapter *modpkg.Adapter) error {
	if !supportsCookieQuery(adapter) {
		return fmt.Errorf("adapter %s does not support cookie queries", adapter.Manifest.Name)
	}
	return store.AssertTrusted(adapter)
}

func supportsCookieQuery(adapter *modpkg.Adapter) bool {
	return adapter.IsRuntime("browser") && (adapter.HasBrowserShare("cookie.query") ||
		(adapter.HasBrowserShare("cookie.list") && adapter.HasBrowserShare("cookie.export")))
}

func invoke(ctx context.Context, adapter *modpkg.Adapter, operation, profile string, args []string, input []byte) ([]byte, error) {
	command, err := adapter.CommandContext(ctx, modpkg.Invocation{Operation: operation, Selection: profile, Arguments: args})
	if err != nil {
		return nil, err
	}
	command.Stdin = bytes.NewReader(input)
	stdout := &boundedBuffer{limit: 8 << 20}
	stderr := &boundedBuffer{limit: 4 << 10}
	command.Stdout = stdout
	command.Stderr = stderr
	err = command.Run()
	if stdout.exceeded || stderr.exceeded {
		return nil, errors.New("browser adapter output exceeds limit")
	}
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			return nil, err
		}
		return nil, errors.New(message)
	}
	return stdout.Bytes(), nil
}

type boundedBuffer struct {
	bytes.Buffer
	limit    int
	exceeded bool
}

func (buffer *boundedBuffer) Write(data []byte) (int, error) {
	if buffer.Len()+len(data) > buffer.limit {
		buffer.exceeded = true
		return 0, errors.New("browser adapter output exceeds limit")
	}
	return buffer.Buffer.Write(data)
}
