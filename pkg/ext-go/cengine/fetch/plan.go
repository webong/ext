package fetch

import (
	"fmt"

	"github.com/webong/ext/pkg/ext-go/cengine/library"
)

// Selection is the exact pinned asset used for one target and driver.
type Selection struct {
	Target string        `json:"target"`
	Driver string        `json:"driver"`
	Asset  library.Asset `json:"asset"`
}

// Plan resolves every requested target/driver pair without downloading,
// consulting caches, or writing files. Order follows targets, then drivers.
// Bundles can inspect this plan before preparing any package or native output.
func Plan(citation library.Citation, drivers, targets []string) ([]Selection, error) {
	if err := citation.Validate(); err != nil {
		return nil, err
	}
	if len(drivers) == 0 || len(targets) == 0 {
		return nil, fmt.Errorf("fetch: drivers and targets are required")
	}
	seenDrivers := map[string]bool{}
	for _, driver := range drivers {
		if driver != CGo && driver != C2Go && driver != PureGo {
			return nil, fmt.Errorf("fetch: unknown driver %q", driver)
		}
		if seenDrivers[driver] {
			return nil, fmt.Errorf("fetch: duplicate driver %q", driver)
		}
		seenDrivers[driver] = true
	}
	seenTargets := map[string]bool{}
	selections := make([]Selection, 0, len(drivers)*len(targets))
	for _, target := range targets {
		if seenTargets[target] {
			return nil, fmt.Errorf("fetch: duplicate target %q", target)
		}
		seenTargets[target] = true
		for _, driver := range drivers {
			asset, err := citation.Asset(target, driver)
			if err != nil {
				return nil, fmt.Errorf("fetch: %s %s publishes no %s build for %s: %w", citation.Library, citation.Version, driver, target, err)
			}
			selections = append(selections, Selection{Target: target, Driver: driver, Asset: asset})
		}
	}
	return selections, nil
}
