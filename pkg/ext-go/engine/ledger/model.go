// Package ledger exposes the C ledger and its shared Go wire model.
package ledger

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"github.com/webong/ext/res/system/capture/surface"
	"github.com/webong/ext/res/system/service/host"
	"github.com/webong/ext/res/system/service/program"
	"net/url"
	"strings"
	"time"
)

const Namespace = "ext.system"

// AdapterObservation describes an installed adapter and any contexts it listed.
// Listed means its discovery operation completed; it does not prove each
// configured remote endpoint is running or authorize its use.
type AdapterObservation struct {
	Name, Runtime, Selector                          string
	DiscoveryStatus                                  string
	Surfaces, Capabilities, Supports                 []string
	Engine, Role                                     []string
	BrowserShare                                     []string
	Trusted, ListSupported, ObserveSupported, Listed bool
	Contexts                                         []ContextObservation
	// Programs are the executables CTX found for the commands this adapter
	// declares. They are machine facts: the graph holds one vertex per file, however
	// many adapters were given it.
	Programs  []program.Candidate
	Resources []ResourceObservation
	Relations []RelationObservation
}

// InstallerObservation declares an installer that is not an adapter, such as one
// the user described in their own configuration. Installers that are adapters
// (role "installer") need no entry here.
type InstallerObservation struct {
	Name, Source string
}

// ContextObservation is a native adapter selection with optional narrower
// capabilities and resource support, plus ordinary non-secret metadata.
type ContextObservation struct {
	Selection    string            `json:"selection"`
	Capabilities []string          `json:"capabilities,omitempty"`
	Supports     []string          `json:"supports,omitempty"`
	BrowserShare map[string]string `json:"browser_share,omitempty"`
	Attributes   map[string]any    `json:"attributes,omitempty"`
}

// ResourceObservation identifies a resource in one observed context. ID is
// adapter-local; the graph stores a digest rather than the raw identifier.
type ResourceObservation struct {
	ID, Kind, Context string
	Attributes        map[string]any
}

// RelationObservation connects two resources reported by the same adapter.
type RelationObservation struct{ From, To, Kind string }

// AliasObservation is a local user declaration that names one adapter context.
type AliasObservation struct {
	Space, Name, Adapter, Selection string
	Metadata                        map[string]string
}

// ContextCandidate is a graph observation, not authorization to run an
// adapter. Callers must still check trust and validate the native selection.
type ContextCandidate struct {
	Adapter           string            `json:"adapter"`
	Runtime           string            `json:"runtime"`
	Selection         string            `json:"selection"`
	SelectionDigest   string            `json:"selection_digest"`
	SelectionRedacted bool              `json:"selection_redacted,omitempty"`
	Capabilities      []string          `json:"capabilities"`
	Supports          []string          `json:"supports,omitempty"`
	Engine            []string          `json:"engine,omitempty"`
	Role              []string          `json:"role,omitempty"`
	BrowserShare      map[string]string `json:"browser_share,omitempty"`
	Program           *ContextProgram   `json:"program,omitempty"`
	// Default marks the adapter the user prefers for the kind a resolve asked for.
	// It filters nothing; a caller that wants one adapter asks for a pick.
	Default    bool      `json:"default,omitempty"`
	ObservedAt time.Time `json:"observed_at"`
}

// ContextProgram is the executable a context runs, as observed. Installers names
// the installer adapters that reported the file; it is empty when the file was
// found on PATH alone. The file may have been replaced since the scan.
type ContextProgram struct {
	Path       string   `json:"path"`
	RealPath   string   `json:"realpath"`
	Version    string   `json:"version,omitempty"`
	OnPath     bool     `json:"on_path,omitempty"`
	Installers []string `json:"installers,omitempty"`
	// Suggested names installers the adapter that runs the file guessed from its
	// location. It is not a fact; see "installers".
	Suggested []string `json:"suggested_installers,omitempty"`
}

// Inventory is the latest reconciled adapter view. ObservedAt is refreshed on
// every scan, even when the set of contexts is unchanged.
type Inventory struct {
	ObservedAt time.Time          `json:"observed_at"`
	Contexts   []ContextCandidate `json:"contexts"`
}

// HostInventory is discovered directly by the graph library, independently of
// adapters. Nil slices and pointers in ObserveHost preserve the corresponding
// inventory; non-nil empty slices remove previously observed entries of that kind.
type HostInventory struct {
	Shells          []host.ShellInfo               `json:"shells"`
	Filesystems     []host.FilesystemInfo          `json:"filesystems"`
	Webviews        []host.WebviewInfo             `json:"webviews"`
	Desktop         *surface.DesktopInfo           `json:"desktop,omitempty"`
	Surfaces        *surface.SurfaceSnapshot       `json:"surfaces,omitempty"`
	Accessibility   *surface.AccessibilitySnapshot `json:"accessibility,omitempty"`
	Permissions     *surface.PermissionSnapshot    `json:"permissions,omitempty"`
	Resources       *host.ResourceInfo             `json:"resources,omitempty"`
	ShellSearchPath *string                        `json:"-"`
}

// OperationRequirements describes host resources needed by a caller. Non-nil
// requirements are conjunctive. Select pins a resource: failure never causes a
// different resource to be substituted. MaxAge defaults to five seconds; a
// negative value forces discovery. No adapter or backend is chosen by name.
type OperationRequirements struct {
	Operation  string                 `json:"operation"`
	Shell      *ShellRequirement      `json:"shell,omitempty"`
	Filesystem *FilesystemRequirement `json:"filesystem,omitempty"`
	Webview    *WebviewRequirement    `json:"webview,omitempty"`
	MaxAge     time.Duration          `json:"-"`
}

type ShellRequirement struct {
	Name       string  `json:"name,omitempty"`
	Select     string  `json:"select,omitempty"` // Executable name or path.
	SearchPath *string `json:"-"`                // Optional effective PATH, without changing the process environment.
}

type FilesystemRequirement struct {
	Select                string   `json:"select,omitempty"` // Mount point.
	Path                  string   `json:"path,omitempty"`   // Existing file/directory or a future output path.
	Types                 []string `json:"types,omitempty"`
	Writable              bool     `json:"writable,omitempty"`
	MinimumAvailableBytes uint64   `json:"minimum_available_bytes,omitempty"`
}

type WebviewRequirement struct {
	Select       string `json:"select,omitempty"` // Discovery location.
	Engine       string `json:"engine,omitempty"`
	API          string `json:"api,omitempty"`
	ABIVersion   string `json:"abi_version,omitempty"`
	Version      string `json:"version,omitempty"` // Exact runtime version, when known.
	Architecture string `json:"architecture,omitempty"`
}

// HostSnapshot is the typed graph view with separate freshness per category.
type HostSnapshot struct {
	HostInventory
	ObservedAt  map[string]time.Time `json:"observed_at"`
	Environment map[string]string    `json:"-"`
}

type HostCandidates struct {
	Operation   string                `json:"operation"`
	Shells      []host.ShellInfo      `json:"shells,omitempty"`
	Filesystems []host.FilesystemInfo `json:"filesystems,omitempty"`
	Webviews    []host.WebviewInfo    `json:"webviews,omitempty"`
	ObservedAt  map[string]time.Time  `json:"observed_at"`
}

// PreparedHost contains one validated resource per requirement. This does not
// authorize execution, reserve capacity, or eliminate races with OS changes.
type PreparedHost struct {
	Operation   string               `json:"operation"`
	Shell       *host.ShellInfo      `json:"shell,omitempty"`
	Filesystem  *host.FilesystemInfo `json:"filesystem,omitempty"`
	Webview     *host.WebviewInfo    `json:"webview,omitempty"`
	ValidatedAt time.Time            `json:"validated_at"`
}

// WebviewValidator belongs to the embedding backend. It must establish that
// this backend can use the selected runtime, including its ABI and dependencies.
type WebviewValidator func(context.Context, host.WebviewInfo) error

// HostRequirementError identifies a failed operation requirement. Cause, when
// present, preserves OS, context, and backend errors for errors.Is/errors.As.
type HostRequirementError struct {
	Operation string
	Kind      string
	Reason    string
	Cause     error
}

func (err *HostRequirementError) Error() string {
	message := err.Reason
	if err.Cause != nil {
		if message != "" {
			message += ": "
		}
		message += err.Cause.Error()
	}
	return fmt.Sprintf("%s: %s requirement: %s", err.Operation, err.Kind, message)
}

func (err *HostRequirementError) Unwrap() error { return err.Cause }

func (inventory Inventory) FreshWithin(maxAge time.Duration) bool {
	return !inventory.ObservedAt.IsZero() && time.Since(inventory.ObservedAt) <= maxAge
}

// Find returns a currently observed context with the requested capabilities.
func (inventory Inventory) Find(adapter, selection string, capabilities ...string) (ContextCandidate, error) {
	for _, candidate := range inventory.Contexts {
		if candidate.Adapter != adapter || (candidate.Selection != selection && candidate.SelectionDigest != digest(selection)) {
			continue
		}
		for _, required := range capabilities {
			found := false
			for _, capability := range candidate.Capabilities {
				if capability == required {
					found = true
					break
				}
			}
			if !found {
				return ContextCandidate{}, fmt.Errorf("%s:%s does not offer %s", adapter, selection, required)
			}
		}
		return candidate, nil
	}
	return ContextCandidate{}, fmt.Errorf("%s:%s is not in the current machine graph", adapter, selection)
}

// SafeOrigin parses a navigation URL and returns only scheme and host.
func SafeOrigin(raw string) (scheme, host string, ok bool) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Hostname() == "" {
		return "", "", false
	}
	scheme = strings.ToLower(parsed.Scheme)
	host = strings.ToLower(parsed.Hostname())
	if parsed.Port() != "" {
		host += ":" + parsed.Port()
	}
	return scheme, host, true
}

func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:12])
}
