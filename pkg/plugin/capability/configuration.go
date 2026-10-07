package capability

import (
	"context"
	"encoding/json"

	"github.com/webong/ext/pkg/plugin"
	"github.com/webong/ext/pkg/plugin/author"
	"github.com/webong/ext/pkg/plugin/instance"
	"github.com/webong/ext/pkg/plugin/schema"
)

type Configuration struct {
	Instance string          `json:"instance"`
	Revision string          `json:"revision"`
	Values   json.RawMessage `json:"values"`
}
type Configured struct {
	Instance string `json:"instance"`
	Revision string `json:"revision"`
}

func ConfigurationMethod() author.Method[Configuration, Configured] {
	return author.Method[Configuration, Configured]{Contract: plugin.ContractRef{Name: "ext.configuration", Version: "v1"}, Operation: plugin.Operation{Name: "update", Surface: "configuration"}, ValidateInput: func(c Configuration) error {
		if c.Instance == "" || len(c.Instance) > 256 || c.Revision == "" || len(c.Revision) > 256 {
			return plugin.ErrInvalid
		}
		var raw json.RawMessage
		return plugin.Decode(c.Values, &raw)
	}}
}

// RegisterConfiguration requires an authenticated scope mapping. The mapper
// derives the manager key from the request's admitted subject and instance ID;
// this prevents two tenants from accidentally sharing a configured resource.
// Configuration values must not contain credentials; hosts own secret delivery.
func RegisterConfiguration[T any](r *author.Registry, manager *instance.Manager[T], configSchema schema.Schema, scope func(context.Context, plugin.Request, string) (string, error)) error {
	if manager == nil || scope == nil {
		return plugin.ErrInvalid
	}
	if err := configSchema.Validate(); err != nil {
		return err
	}
	configSchema = configSchema.Clone()
	return author.Register(r, ConfigurationMethod(), func(ctx context.Context, request plugin.Request, input Configuration) (Configured, error) {
		if err := configSchema.Check(input.Values); err != nil {
			return Configured{}, &plugin.RemoteError{Code: "invalid_config", Message: "configuration does not satisfy schema"}
		}
		key, err := scope(ctx, request, input.Instance)
		if err != nil || key == "" {
			return Configured{}, &plugin.RemoteError{Code: "denied", Message: "configuration denied"}
		}
		if err := manager.Configure(ctx, key, input.Revision, input.Values); err != nil {
			return Configured{}, err
		}
		return Configured{Instance: input.Instance, Revision: input.Revision}, nil
	})
}
