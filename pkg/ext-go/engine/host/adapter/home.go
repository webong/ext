package adapter

import hosthome "github.com/webong/ext/pkg/ext-go/engine/host/home"

// ConfigHome is the root of ext's per-user state, shared by every product so
// that ctx and ctn see the same installed adapters and the same trust
// decisions. EXT_HOME overrides it. CTX_HOME is honored for installations
// that predate the shared home.
func ConfigHome() string {
	return hosthome.Config()
}

// Home is the directory holding installed adapters and their trust records. It
// is global to the user: trusting an adapter once makes it available to every
// product. EXT_ADAPTER_HOME overrides it, then EXT_HOME. The CTX_ADAPTER_HOME
// and CTX_HOME names remain accepted for earlier installations.
func Home() string {
	return hosthome.Adapters()
}
