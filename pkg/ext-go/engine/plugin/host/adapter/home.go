package adapter

import hosthome "github.com/webong/ext/pkg/ext-go/engine/plugin/host/home"

// ConfigHome is the root of ext's per-user state, shared by every product so
// that ctx and ctn see the same installed adapters and the same trust
// decisions. EXT_HOME overrides it.
func ConfigHome() string {
	return hosthome.Config()
}

// Home is the shared package directory, viewed through adapter contracts. It
// is global to the user: trusting an adapter once makes it available to every
// product. EXT_ADAPTER_HOME overrides it, then EXT_HOME.
func Home() string {
	return hosthome.Adapters()
}
