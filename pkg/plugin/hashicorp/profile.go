package hashicorp

import "github.com/webong/ctx/pkg/plugin"

func GRPCProfile() plugin.BackendProfile {
	return plugin.BackendProfile{Name: "hashicorp/grpc", Protocols: []string{plugin.APIVersion}, Concurrent: true, Cancellation: "request", ProcessOwner: "go-plugin"}
}

func NetRPCProfile() plugin.BackendProfile {
	return plugin.BackendProfile{Name: "hashicorp/netrpc", Protocols: []string{plugin.APIVersion}, Concurrent: true, Cancellation: "connection", ProcessOwner: "go-plugin"}
}
