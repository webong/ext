package jsonline

import "github.com/webong/ext/pkg/plugin"

func Profile() plugin.BackendProfile {
	return plugin.BackendProfile{Name: "jsonline", Protocols: []string{plugin.APIVersion}, Cancellation: "connection", ProcessOwner: "host"}
}
