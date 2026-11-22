package hashicorp

import plugin "github.com/webong/ext/pkg/ext-go/engine/plugin"

func GRPCProfile() plugin.BackendProfile {
	return plugin.BackendProfile{Name: "hashicorp/grpc", Protocols: []string{plugin.APIVersion}, Concurrent: true, Cancellation: "request", ProcessOwner: "go-plugin", Boundary: plugin.BoundaryProcess}
}

func NetRPCProfile() plugin.BackendProfile {
	return plugin.BackendProfile{Name: "hashicorp/netrpc", Protocols: []string{plugin.APIVersion}, Concurrent: true, Cancellation: "connection", ProcessOwner: "go-plugin", Boundary: plugin.BoundaryProcess}
}
