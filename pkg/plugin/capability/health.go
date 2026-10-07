// Package capability contains optional, independently versioned SDK contracts.
package capability

import (
	"context"

	"github.com/webong/ext/pkg/plugin"
	"github.com/webong/ext/pkg/plugin/author"
	"github.com/webong/ext/pkg/plugin/schema"
)

type HealthRequest struct{}
type Health struct {
	Status string `json:"status"`
	Ready  bool   `json:"ready"`
}

func HealthMethod() author.Method[HealthRequest, Health] {
	return author.Method[HealthRequest, Health]{Contract: plugin.ContractRef{Name: "ext.health", Version: "v1"}, Operation: plugin.Operation{Name: "check", Surface: "observation"}, Input: &schema.Schema{Type: "object"}, Output: &schema.Schema{Type: "object", Properties: map[string]schema.Schema{"status": {Type: "string", Enum: []string{"healthy", "degraded", "unhealthy"}}, "ready": {Type: "boolean"}}, Required: []string{"status", "ready"}}}
}
func RegisterHealth(r *author.Registry, check func(context.Context) (Health, error)) error {
	if check == nil {
		return plugin.ErrInvalid
	}
	return author.Register(r, HealthMethod(), func(ctx context.Context, _ plugin.Request, _ HealthRequest) (Health, error) { return check(ctx) })
}
