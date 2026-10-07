package adapter

import (
	"os"
	"path/filepath"
	"runtime"
)

// ConfigHome is the root of ext's per-user state, shared by every product so
// that ctx and ctn see the same installed adapters and the same trust
// decisions. EXT_HOME overrides it. CTX_HOME is honored for installations
// that predate the shared home.
func ConfigHome() string {
	if home := os.Getenv("EXT_HOME"); home != "" {
		return home
	}
	if home := os.Getenv("CTX_HOME"); home != "" {
		return home
	}
	return defaultHome()
}

// Home is the directory holding installed adapters and their trust records. It
// is global to the user: trusting an adapter once makes it available to every
// product. EXT_ADAPTER_HOME overrides it, then EXT_HOME. The CTX_ADAPTER_HOME
// and CTX_HOME names remain accepted for earlier installations.
func Home() string {
	for _, name := range []string{"EXT_ADAPTER_HOME", "CTX_ADAPTER_HOME"} {
		if home := os.Getenv(name); home != "" {
			return home
		}
	}
	return filepath.Join(ConfigHome(), "adapters")
}

// defaultHome prefers the shared ext directory. An existing ctx directory is
// kept only while the ext directory has not been created, so an earlier
// installation continues to work without being moved.
func defaultHome() string {
	shared, legacy := homeCandidates()
	if shared == "" {
		return ".ext"
	}
	if _, err := os.Stat(shared); err == nil {
		return shared
	}
	if legacy != "" {
		if _, err := os.Stat(legacy); err == nil {
			return legacy
		}
	}
	return shared
}

func homeCandidates() (shared, legacy string) {
	if runtime.GOOS == "windows" {
		if dir, err := os.UserConfigDir(); err == nil {
			return filepath.Join(dir, "ext"), filepath.Join(dir, "ctx")
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		if runtime.GOOS == "windows" {
			return filepath.Join(home, ".ext"), filepath.Join(home, ".ctx")
		}
		return filepath.Join(home, ".config", "ext"), filepath.Join(home, ".config", "ctx")
	}
	return "", ""
}
