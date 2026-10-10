// Package packagekit supplies portable manifests and dependency preflight.
// Hosts own discovery, trust storage and activation; adapters own native tools.
package packagekit

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/webong/ext/pkg/graph"
	"github.com/webong/ext/pkg/plugin"
	"github.com/webong/ext/pkg/plugin/schema"
)

const Version = "ext.package/v1"

var namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._/-]{0,255}$`)
var devicePattern = regexp.MustCompile(`(?i)^(con|prn|aux|nul|com[1-9]|lpt[1-9])(?:\.|$)`)

var digestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

type Artifact struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	OS     string `json:"os,omitempty"`
	Arch   string `json:"arch,omitempty"`
}
type Entrypoint struct {
	Name      string   `json:"name"`
	Runtime   string   `json:"runtime"`
	Artifact  string   `json:"artifact"`
	Protocols []string `json:"protocols"`
}
type Dependency struct {
	Contract  plugin.ContractRef `json:"contract"`
	Operation string             `json:"operation"`
	Identity  *plugin.Identity   `json:"identity,omitempty"`
}
type Asset struct {
	Artifact string `json:"artifact"`
	Kind     string `json:"kind"`
	Locale   string `json:"locale,omitempty"`
}

// SharedDependency declares exact host-provided frontend versions accepted by
// this package. Hosts resolve richer release ranges before constructing this.
type SharedDependency struct {
	Name     string   `json:"name"`
	Versions []string `json:"versions"`
}
type PayloadSchema struct {
	Contract  plugin.ContractRef `json:"contract"`
	Operation string             `json:"operation"`
	Input     *schema.Schema     `json:"input,omitempty"`
	Output    *schema.Schema     `json:"output,omitempty"`
}
type Manifest struct {
	APIVersion         string             `json:"apiVersion"`
	Descriptor         plugin.Descriptor  `json:"descriptor"`
	Artifacts          []Artifact         `json:"artifacts"`
	Entrypoints        []Entrypoint       `json:"entrypoints,omitempty"`
	Requires           []Dependency       `json:"requires,omitempty"`
	Configuration      *schema.Schema     `json:"configuration,omitempty"`
	Payloads           []PayloadSchema    `json:"payloads,omitempty"`
	Assets             []Asset            `json:"assets,omitempty"`
	SharedDependencies []SharedDependency `json:"sharedDependencies,omitempty"`
}

func portablePath(value string) bool {
	if !fs.ValidPath(value) || value == "." || path.Clean(value) != value || strings.ContainsAny(value, "\\:\x00\r\n") || len(value) > 1024 {
		return false
	}
	for _, part := range strings.Split(value, "/") {
		if strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") || devicePattern.MatchString(part) {
			return false
		}
	}
	return true
}
func (m Manifest) Validate() error {
	if m.APIVersion != Version {
		return plugin.ErrInvalid
	}
	if err := m.Descriptor.Validate(); err != nil {
		return err
	}
	if len(m.Artifacts) < 1 || len(m.Artifacts) > 4096 || len(m.Entrypoints) > 64 || len(m.Requires) > 256 || len(m.Assets) > 1024 || len(m.Payloads) > 1024 || len(m.SharedDependencies) > 256 {
		return plugin.ErrInvalid
	}
	names := map[string]bool{}
	paths := map[string]bool{}
	for _, a := range m.Artifacts {
		if !namePattern.MatchString(a.Name) || names[a.Name] || !portablePath(a.Path) || paths[strings.ToLower(a.Path)] || !digestPattern.MatchString(a.SHA256) || (a.OS != "" && !namePattern.MatchString(a.OS)) || (a.Arch != "" && !namePattern.MatchString(a.Arch)) {
			return fmt.Errorf("%w: invalid artifact", plugin.ErrInvalid)
		}
		names[a.Name] = true
		paths[strings.ToLower(a.Path)] = true
	}
	entries := map[string]bool{}
	for _, e := range m.Entrypoints {
		if !namePattern.MatchString(e.Name) || entries[e.Name] || !namePattern.MatchString(e.Runtime) || !names[e.Artifact] {
			return plugin.ErrInvalid
		}
		entries[e.Name] = true
		if _, err := plugin.NegotiateProtocol(e.Protocols, e.Protocols); err != nil {
			return err
		}
	}
	requirements := map[string]bool{}
	for _, d := range m.Requires {
		raw, _ := json.Marshal(d)
		key := string(raw)
		if requirements[key] {
			return plugin.ErrInvalid
		}
		requirements[key] = true
		if err := d.Contract.Validate(); err != nil {
			return err
		}
		if !namePattern.MatchString(d.Operation) {
			return plugin.ErrInvalid
		}
		if d.Identity != nil {
			if err := d.Identity.Validate(); err != nil {
				return err
			}
		}
	}
	if m.Configuration != nil {
		if err := m.Configuration.Validate(); err != nil {
			return err
		}
	}
	schemas := map[string]bool{}
	for _, p := range m.Payloads {
		if _, err := m.Descriptor.Lookup(p.Contract, p.Operation); err != nil {
			return err
		}
		k := p.Contract.Name + "@" + p.Contract.Version + ":" + p.Operation
		if schemas[k] {
			return plugin.ErrInvalid
		}
		schemas[k] = true
		for _, s := range []*schema.Schema{p.Input, p.Output} {
			if s != nil {
				if err := s.Validate(); err != nil {
					return err
				}
			}
		}
	}
	for _, a := range m.Assets {
		if !names[a.Artifact] || !namePattern.MatchString(a.Kind) || len(a.Locale) > 64 {
			return plugin.ErrInvalid
		}
	}
	shared := map[string]bool{}
	for _, d := range m.SharedDependencies {
		if d.Name == "" || len(d.Name) > 256 || shared[d.Name] || len(d.Versions) == 0 || len(d.Versions) > 64 {
			return plugin.ErrInvalid
		}
		shared[d.Name] = true
		seen := map[string]bool{}
		for _, v := range d.Versions {
			if v == "" || len(v) > 256 || seen[v] {
				return plugin.ErrInvalid
			}
			seen[v] = true
		}
	}
	return nil
}
func Decode(data []byte) (Manifest, error) {
	var m Manifest
	if err := plugin.Decode(data, &m); err != nil {
		return m, err
	}
	return m, m.Validate()
}

// VerifyArtifacts checks listed contents only. Callers must protect the package
// against concurrent replacement and authenticate the reviewed manifest itself.
// A checksum never establishes publisher trust. No executable is launched.
func (m Manifest) VerifyArtifacts(root string) error {
	if err := m.Validate(); err != nil {
		return err
	}
	info, err := os.Lstat(root)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return plugin.ErrInvalid
	}
	for _, a := range m.Artifacts {
		current := root
		parts := strings.Split(a.Path, "/")
		for i, part := range parts {
			current = filepath.Join(current, part)
			info, err = os.Lstat(current)
			if err != nil {
				return err
			}
			if info.Mode()&os.ModeSymlink != 0 || (i < len(parts)-1 && !info.IsDir()) || (i == len(parts)-1 && !info.Mode().IsRegular()) {
				return plugin.ErrInvalid
			}
		}
		f, err := os.Open(current)
		if err != nil {
			return err
		}
		h := sha256.New()
		_, readErr := io.Copy(h, f)
		closeErr := f.Close()
		if readErr != nil {
			return readErr
		}
		if closeErr != nil {
			return closeErr
		}
		if hex.EncodeToString(h.Sum(nil)) != a.SHA256 {
			return fmt.Errorf("%w: artifact %s", plugin.ErrMismatch, a.Name)
		}
	}
	return nil
}

type Resolution struct {
	Consumer    plugin.Identity `json:"consumer"`
	Requirement Dependency      `json:"requirement"`
	Provider    plugin.Identity `json:"provider"`
}
type Plan struct {
	Order    []plugin.Identity `json:"order"`
	Bindings []Resolution      `json:"bindings"`
}

// Resolve requires unique exact providers and rejects dependency cycles. It
// returns provider-before-consumer order; it neither executes nor grants access.
func Resolve(manifests []Manifest) (Plan, error) {
	if len(manifests) > 1024 {
		return Plan{}, plugin.ErrInvalid
	}
	descriptors := make([]plugin.Descriptor, len(manifests))
	index := map[plugin.Identity]int{}
	for i, m := range manifests {
		if err := m.Validate(); err != nil {
			return Plan{}, err
		}
		if _, ok := index[m.Descriptor.Identity]; ok {
			return Plan{}, plugin.ErrAmbiguous
		}
		index[m.Descriptor.Identity] = i
		descriptors[i] = m.Descriptor
	}
	p := Plan{}
	dependencies := make([][]int, len(manifests))
	for i, m := range manifests {
		for _, r := range m.Requires {
			d, err := plugin.Select(descriptors, plugin.Requirement{Contract: r.Contract, Operation: r.Operation, Identity: r.Identity})
			if err != nil {
				return Plan{}, fmt.Errorf("%s requires %s@%s/%s: %w", m.Descriptor.Identity.ID, r.Contract.Name, r.Contract.Version, r.Operation, err)
			}
			dependencies[i] = append(dependencies[i], index[d.Identity])
			if r.Identity != nil {
				id := *r.Identity
				r.Identity = &id
			}
			p.Bindings = append(p.Bindings, Resolution{Consumer: m.Descriptor.Identity, Requirement: r, Provider: d.Identity})
		}
	}
	state := make([]int, len(manifests))
	var visit func(int) error
	visit = func(i int) error {
		if state[i] == 1 {
			return fmt.Errorf("%w: plugin dependency cycle", plugin.ErrInvalid)
		}
		if state[i] == 2 {
			return nil
		}
		state[i] = 1
		for _, j := range dependencies[i] {
			if err := visit(j); err != nil {
				return err
			}
		}
		state[i] = 2
		p.Order = append(p.Order, descriptors[i].Identity)
		return nil
	}
	order := make([]int, len(manifests))
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(i, j int) bool {
		return identityKey(descriptors[order[i]].Identity) < identityKey(descriptors[order[j]].Identity)
	})
	for _, i := range order {
		if err := visit(i); err != nil {
			return Plan{}, err
		}
	}
	return p, nil
}
func identityKey(id plugin.Identity) string {
	data, _ := json.Marshal(id)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// Graph creates a transaction for the caller's namespace. Apply it through the
// host's graph policy. Records express dependencies, never permissions.
func (p Plan) Graph(namespace string) graph.Transaction {
	tx := graph.Transaction{Namespace: namespace}
	for _, id := range p.Order {
		tx.Vertices = append(tx.Vertices, graph.Vertex{Namespace: namespace, ID: identityKey(id), Kind: "plugin", Attributes: map[string]any{"package": id.ID, "revision": id.Revision, "version": id.Version}})
	}
	for i, b := range p.Bindings {
		tx.Edges = append(tx.Edges, graph.Edge{Namespace: namespace, ID: fmt.Sprintf("requires-%s-%d", identityKey(b.Consumer), i), From: identityKey(b.Consumer), To: identityKey(b.Provider), Type: "requires", Attributes: map[string]any{"contract": b.Requirement.Contract.Name, "version": b.Requirement.Contract.Version, "operation": b.Requirement.Operation}})
	}
	return tx
}

// Environment is supplied by the host. Runtime profiles describe installed
// implementations; shared versions describe libraries the host actually serves.
type Environment struct {
	OS             string
	Arch           string
	Runtimes       map[string]plugin.BackendProfile
	SharedVersions map[string]string
}
type LaunchSelection struct {
	Entrypoint Entrypoint `json:"entrypoint"`
	Artifact   Artifact   `json:"artifact"`
	Protocol   string     `json:"protocol"`
}

func (m Manifest) SelectEntrypoint(name string, env Environment) (LaunchSelection, error) {
	if err := m.Validate(); err != nil {
		return LaunchSelection{}, err
	}
	for _, d := range m.SharedDependencies {
		found := false
		for _, v := range d.Versions {
			if env.SharedVersions[d.Name] == v {
				found = true
			}
		}
		if !found {
			return LaunchSelection{}, fmt.Errorf("%w: host dependency %s", plugin.ErrUnsupported, d.Name)
		}
	}
	for _, e := range m.Entrypoints {
		if e.Name != name {
			continue
		}
		profile, ok := env.Runtimes[e.Runtime]
		if !ok {
			return LaunchSelection{}, fmt.Errorf("%w: runtime %s", plugin.ErrUnsupported, e.Runtime)
		}
		protocol, err := plugin.NegotiateProtocol(profile.Protocols, e.Protocols)
		if err != nil {
			return LaunchSelection{}, err
		}
		for _, a := range m.Artifacts {
			if a.Name == e.Artifact {
				if (a.OS != "" && a.OS != env.OS) || (a.Arch != "" && a.Arch != env.Arch) {
					return LaunchSelection{}, fmt.Errorf("%w: artifact platform", plugin.ErrUnsupported)
				}
				e.Protocols = append([]string(nil), e.Protocols...)
				return LaunchSelection{e, a, protocol}, nil
			}
		}
	}
	return LaunchSelection{}, plugin.ErrNotFound
}

// SourceOnly reports whether the package ships payload files with no declared
// entrypoint. The consumer chooses the runtime and the file that starts it.
func (m Manifest) SourceOnly() bool { return len(m.Entrypoints) == 0 }

// Artifact returns the listed artifact with the given name.
func (m Manifest) Artifact(name string) (Artifact, bool) {
	for _, a := range m.Artifacts {
		if a.Name == name {
			return a, true
		}
	}
	return Artifact{}, false
}
