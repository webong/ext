package inference

import (
	"fmt"
	"sort"
	"sync"
)

// ModelCatalog records explicit caller-supplied model bindings. It performs
// no discovery, endpoint selection, fallback, credential lookup, or retries.
type ModelCatalog struct {
	mu     sync.RWMutex
	models map[string]modelBinding
}

type modelBinding struct {
	info     ModelInfo
	provider Provider
}

func (c *ModelCatalog) Register(info ModelInfo, provider Provider) error {
	if c == nil || info.ID == "" || info.Provider == "" || provider == nil {
		return fmt.Errorf("%w: model ID, provider identity, and binding are required", ErrInvalidRequest)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.models == nil {
		c.models = map[string]modelBinding{}
	}
	if _, exists := c.models[info.ID]; exists {
		return fmt.Errorf("%w: model %q is already registered", ErrInvalidRequest, info.ID)
	}
	info.Capabilities = append([]Capability(nil), info.Capabilities...)
	info.Modalities = append([]Modality(nil), info.Modalities...)
	info.Devices = append([]Device(nil), info.Devices...)
	c.models[info.ID] = modelBinding{info: info, provider: provider}
	return nil
}

func (c *ModelCatalog) Lookup(id string) (ModelInfo, Provider, bool) {
	if c == nil {
		return ModelInfo{}, nil, false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	binding, found := c.models[id]
	if !found {
		return ModelInfo{}, nil, false
	}
	info := binding.info
	info.Capabilities = append([]Capability(nil), info.Capabilities...)
	info.Modalities = append([]Modality(nil), info.Modalities...)
	info.Devices = append([]Device(nil), info.Devices...)
	return info, binding.provider, true
}

func (c *ModelCatalog) Models() []ModelInfo {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	models := make([]ModelInfo, 0, len(c.models))
	for _, binding := range c.models {
		info := binding.info
		info.Capabilities = append([]Capability(nil), info.Capabilities...)
		info.Modalities = append([]Modality(nil), info.Modalities...)
		info.Devices = append([]Device(nil), info.Devices...)
		models = append(models, info)
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	return models
}
