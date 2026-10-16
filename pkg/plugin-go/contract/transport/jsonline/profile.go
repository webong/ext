package jsonline

import plugin "github.com/webong/ext/pkg/plugin-go/contract"

func Profile() plugin.BackendProfile {
	return plugin.BackendProfile{Name: "jsonline", Protocols: []string{plugin.APIVersion}, Cancellation: "connection", ProcessOwner: "host"}
}
