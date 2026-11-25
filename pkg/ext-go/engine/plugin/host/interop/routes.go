// Package interop preserves the published route-planning API.
// Shared metadata and portable resolution live in core/plugin/interop.
package interop

import (
	plugin "github.com/webong/ext/pkg/ext-go/core/plugin"
	shared "github.com/webong/ext/pkg/ext-go/core/plugin/interop"
)

type Bridge = shared.Bridge
type Requirements = shared.Requirements
type Route = shared.Route

func Resolve(hosts, guests []plugin.BackendProfile, bridges []Bridge, requirements Requirements) (Route, error) {
	return shared.Resolve(hosts, guests, bridges, requirements)
}
