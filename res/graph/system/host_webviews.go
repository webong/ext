package systemgraph

import (
	"context"
	"fmt"
	"sort"
)

// WebviewInfo describes evidence of a shared web rendering runtime. Discovery
// does not load the runtime or establish that a particular app can use it.
// Location is a framework/library path or a Windows registry location.
// Version is the runtime version; ABIVersion is a library API/ABI generation,
// not the underlying WebKit or Chromium version.
type WebviewInfo struct {
	Name         string `json:"name"`
	Engine       string `json:"engine"`
	API          string `json:"api"`
	Version      string `json:"version,omitempty"`
	ABIVersion   string `json:"abi_version,omitempty"`
	Architecture string `json:"architecture,omitempty"`
	Location     string `json:"location"`
	ResolvedPath string `json:"resolved_path,omitempty"`
	Scope        string `json:"scope"`
	Source       string `json:"source"`
	DetailError  string `json:"detail_error,omitempty"`
}

// DiscoverWebviews inventories host webview runtimes without launching a
// browser, reading app profiles, or depending on installed CTX adapters.
func DiscoverWebviews(ctx context.Context) ([]WebviewInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	items, err := platformWebviews(ctx)
	if err != nil {
		return nil, err
	}
	if items == nil {
		items = []WebviewInfo{}
	}
	seen := map[string]bool{}
	result := make([]WebviewInfo, 0, len(items))
	for _, item := range items {
		if item.Name == "" || item.Engine == "" || item.API == "" || item.Location == "" || item.Scope == "" || item.Source == "" {
			return nil, fmt.Errorf("incomplete host webview observation")
		}
		key := webviewIdentity(item)
		if !seen[key] {
			result = append(result, item)
			seen[key] = true
		}
	}
	sort.Slice(result, func(i, j int) bool { return webviewIdentity(result[i]) < webviewIdentity(result[j]) })
	return result, ctx.Err()
}

func webviewIdentity(item WebviewInfo) string {
	return item.Name + "\x00" + item.Scope + "\x00" + hostPathKey(item.Location)
}
