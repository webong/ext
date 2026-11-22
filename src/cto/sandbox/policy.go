package sandbox

import (
	"strings"
)

// The bounds of a policy (docs/adr-architecture.md, rule 4). testdata/v1 states the same
// numbers under "limits".
const (
	MaxPaths       = 256
	MaxPathBytes   = 4096
	MaxPorts       = 256
	MaxHosts       = 256
	MaxHostBytes   = 253
	MaxDevices     = 64
	MaxDeviceBytes = 128
	MaxEnvNames    = 256
	MaxEnvBytes    = 32768
	MaxNameBytes   = 256
	MaxRefBytes    = 1024
)

// Access is one kind of access to a path.
type Access string

// The kinds of access, in their canonical order.
const (
	Read    Access = "read"
	Write   Access = "write"
	Execute Access = "execute"
)

var accessOrder = []Access{Read, Write, Execute}

// Path grants or requests access to one absolute path and everything below it.
type Path struct {
	// Path is absolute and slash-separated: "/" and its descendants, or a drive such as "C:/"
	// and its descendants. It has no empty, "." or ".." component and no trailing slash except
	// at a root.
	Path   string   `json:"path"`
	Access []Access `json:"access"`
}

// The network modes. An empty mode is None.
const (
	// NetworkNone reaches nothing.
	NetworkNone = "none"
	// NetworkPorts reaches the listed TCP ports on any address, where a boundary can enforce
	// ports without hostnames.
	NetworkPorts = "ports"
	// NetworkProxy reaches only an egress proxy that allows the listed hosts.
	NetworkProxy = "proxy"
	// NetworkOpen reaches anything. A grant with an open network is never sandboxed.
	NetworkOpen = "open"
)

// Host is one destination an egress proxy allows: an exact lowercase hostname or address and a
// TCP port.
type Host struct {
	Host string `json:"host"`
	Port int    `json:"port"`
}

// Network is what the code can reach.
type Network struct {
	Mode  string `json:"mode,omitempty"`
	Ports []int  `json:"ports,omitempty"`
	Hosts []Host `json:"hosts,omitempty"`
}

// ProcessPolicy says whether the code may start other processes. No new privileges is always
// enforced and is not a field.
type ProcessPolicy struct {
	Children bool `json:"children,omitempty"`
}

// Resources are bounds. A nil field sets no bound; an explicit zero or negative value is
// invalid.
type Resources struct {
	MemoryBytes *int64 `json:"memoryBytes,omitempty"`
	MilliCPU    *int64 `json:"milliCpu,omitempty"`
	Pids        *int64 `json:"pids,omitempty"`
	CPUTimeMs   *int64 `json:"cpuTimeMs,omitempty"`
	WallTimeMs  *int64 `json:"wallTimeMs,omitempty"`
	OpenFiles   *int64 `json:"openFiles,omitempty"`
	WriteBytes  *int64 `json:"writeBytes,omitempty"`
}

// Secret names an environment variable whose value the runner resolves from Ref at start. A
// policy never carries a secret's value.
type Secret struct {
	Name string `json:"name"`
	Ref  string `json:"ref"`
}

// Environment is the environment beyond the runner's scrubbed base. A request lists the Names
// it needs; a policy supplies Entries ("KEY=value") and Secrets.
type Environment struct {
	Names   []string `json:"names,omitempty"`
	Entries []string `json:"entries,omitempty"`
	Secrets []Secret `json:"secrets,omitempty"`
}

// The lifecycle modes. An empty mode is Fresh.
const (
	// LifecycleFresh starts from new state, deleted afterwards.
	LifecycleFresh = "fresh"
	// LifecycleWorkspace keeps a named directory or volume across runs.
	LifecycleWorkspace = "workspace"
	// LifecycleSnapshot restores a named snapshot before the run.
	LifecycleSnapshot = "snapshot"
)

// The scopes a snapshot restores.
const (
	// RestoreFilesystem restores files only.
	RestoreFilesystem = "filesystem"
	// RestoreMemory restores memory and device state as well, as a VM snapshot does.
	RestoreMemory = "memory"
)

// Lifecycle is what survives between runs. Only a policy names the workspace or snapshot; the
// host chooses where state lives.
type Lifecycle struct {
	Mode    string `json:"mode,omitempty"`
	Name    string `json:"name,omitempty"`
	Restore string `json:"restore,omitempty"`
}

// Policy is what code can touch inside a boundary. The same shape serves as a request (what a
// package needs), a policy (what a consumer allows) and a grant (their overlap). Everything not
// listed is denied.
type Policy struct {
	Filesystem  []Path        `json:"filesystem,omitempty"`
	Network     Network       `json:"network,omitzero"`
	Process     ProcessPolicy `json:"process,omitzero"`
	Devices     []string      `json:"devices,omitempty"`
	Resources   Resources     `json:"resources,omitzero"`
	Environment Environment   `json:"environment,omitzero"`
	Lifecycle   Lifecycle     `json:"lifecycle,omitzero"`
}

// Validate checks the shape and bounds of p. It does not decide whether p is acceptable: that
// is Grant and Enforce.
func (p Policy) Validate() error {
	if len(p.Filesystem) > MaxPaths {
		return ErrInvalid
	}
	seenPaths := map[string]bool{}
	for _, entry := range p.Filesystem {
		if !validPath(entry.Path) || seenPaths[entry.Path] || !validAccess(entry.Access) {
			return ErrInvalid
		}
		seenPaths[entry.Path] = true
	}
	if err := p.Network.validate(); err != nil {
		return err
	}
	if len(p.Devices) > MaxDevices {
		return ErrInvalid
	}
	seenDevices := map[string]bool{}
	for _, device := range p.Devices {
		if !validDevice(device) || seenDevices[device] {
			return ErrInvalid
		}
		seenDevices[device] = true
	}
	for _, value := range p.Resources.fields() {
		if value.v != nil && *value.v <= 0 {
			return ErrInvalid
		}
	}
	if err := p.Environment.validate(); err != nil {
		return err
	}
	return p.Lifecycle.validate()
}

func (n Network) validate() error {
	switch n.Mode {
	case "", NetworkNone, NetworkOpen:
		if len(n.Ports) > 0 || len(n.Hosts) > 0 {
			return ErrInvalid
		}
	case NetworkPorts:
		if len(n.Hosts) > 0 || len(n.Ports) > MaxPorts {
			return ErrInvalid
		}
		seen := map[int]bool{}
		for _, port := range n.Ports {
			if !validPort(port) || seen[port] {
				return ErrInvalid
			}
			seen[port] = true
		}
	case NetworkProxy:
		if len(n.Ports) > 0 || len(n.Hosts) > MaxHosts {
			return ErrInvalid
		}
		seen := map[Host]bool{}
		for _, host := range n.Hosts {
			if !validHost(host.Host) || !validPort(host.Port) || seen[host] {
				return ErrInvalid
			}
			seen[host] = true
		}
	default:
		return ErrInvalid
	}
	return nil
}

func (e Environment) validate() error {
	if len(e.Names) > MaxEnvNames || len(e.Entries)+len(e.Secrets) > MaxEnvNames {
		return ErrInvalid
	}
	names := map[string]bool{}
	for _, name := range e.Names {
		if !validEnvName(name) || names[name] {
			return ErrInvalid
		}
		names[name] = true
	}
	supplied := map[string]bool{}
	for _, entry := range e.Entries {
		name, _, ok := strings.Cut(entry, "=")
		if !ok || len(entry) > MaxEnvBytes || strings.ContainsRune(entry, 0) || !validEnvName(name) || supplied[name] {
			return ErrInvalid
		}
		supplied[name] = true
	}
	for _, secret := range e.Secrets {
		if !validEnvName(secret.Name) || supplied[secret.Name] || secret.Ref == "" || len(secret.Ref) > MaxRefBytes || strings.ContainsRune(secret.Ref, 0) {
			return ErrInvalid
		}
		supplied[secret.Name] = true
	}
	return nil
}

func (l Lifecycle) validate() error {
	switch l.Mode {
	case "", LifecycleFresh:
		if l.Name != "" || l.Restore != "" {
			return ErrInvalid
		}
	case LifecycleWorkspace:
		if l.Restore != "" || (l.Name != "" && !validName(l.Name)) {
			return ErrInvalid
		}
	case LifecycleSnapshot:
		if l.Restore != RestoreFilesystem && l.Restore != RestoreMemory {
			return ErrInvalid
		}
		if l.Name != "" && !validName(l.Name) {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	return nil
}

type resourceField struct {
	name string
	v    *int64
}

func (r Resources) fields() []resourceField {
	return []resourceField{
		{"memoryBytes", r.MemoryBytes},
		{"milliCpu", r.MilliCPU},
		{"pids", r.Pids},
		{"cpuTimeMs", r.CPUTimeMs},
		{"wallTimeMs", r.WallTimeMs},
		{"openFiles", r.OpenFiles},
		{"writeBytes", r.WriteBytes},
	}
}

func (r *Resources) set(name string, v *int64) {
	switch name {
	case "memoryBytes":
		r.MemoryBytes = v
	case "milliCpu":
		r.MilliCPU = v
	case "pids":
		r.Pids = v
	case "cpuTimeMs":
		r.CPUTimeMs = v
	case "wallTimeMs":
		r.WallTimeMs = v
	case "openFiles":
		r.OpenFiles = v
	case "writeBytes":
		r.WriteBytes = v
	}
}

// pathRoot returns the length of the root of p ("/" or "X:/"), or 0 when p is not absolute.
func pathRoot(p string) int {
	if strings.HasPrefix(p, "/") {
		return 1
	}
	if len(p) >= 3 && p[0] >= 'A' && p[0] <= 'Z' && p[1] == ':' && p[2] == '/' {
		return 3
	}
	return 0
}

func validPath(p string) bool {
	if p == "" || len(p) > MaxPathBytes || strings.ContainsAny(p, "\x00\\") {
		return false
	}
	root := pathRoot(p)
	if root == 0 {
		return false
	}
	rest := p[root:]
	if rest == "" {
		return true
	}
	for _, component := range strings.Split(rest, "/") {
		if component == "" || component == "." || component == ".." {
			return false
		}
	}
	return true
}

// within reports whether p is base or below it. Both are valid paths.
func within(p, base string) bool {
	if p == base {
		return true
	}
	if pathRoot(base) == len(base) {
		return strings.HasPrefix(p, base)
	}
	return strings.HasPrefix(p, base+"/")
}

func validAccess(access []Access) bool {
	if len(access) == 0 || len(access) > len(accessOrder) {
		return false
	}
	seen := map[Access]bool{}
	for _, a := range access {
		if (a != Read && a != Write && a != Execute) || seen[a] {
			return false
		}
		seen[a] = true
	}
	return true
}

func validPort(port int) bool { return port >= 1 && port <= 65535 }

// validHost accepts a lowercase hostname or address of letters, digits, '-' and '.', in labels
// of at most 63 bytes.
func validHost(host string) bool {
	if host == "" || len(host) > MaxHostBytes {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for i := 0; i < len(label); i++ {
			c := label[i]
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}

func validDevice(name string) bool {
	if name == "" || len(name) > MaxDeviceBytes {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-' || c == '/') {
			return false
		}
	}
	return true
}

func validEnvName(name string) bool {
	if name == "" || len(name) > MaxNameBytes {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if !(c == '_' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || i > 0 && c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

func validName(name string) bool {
	if name == "" || len(name) > MaxNameBytes || name == "." || name == ".." {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}
