package sandbox

// Refusal is one part of a request that the policy did not allow. Class names the policy
// class (see Classes); the other fields say which item, where the class has items.
type Refusal struct {
	Class  string   `json:"class"`
	Path   string   `json:"path,omitempty"`
	Access []Access `json:"access,omitempty"`
	Host   *Host    `json:"host,omitempty"`
	Port   int      `json:"port,omitempty"`
	Name   string   `json:"name,omitempty"`
}

// Grant returns the overlap of a request (what a package needs) and a policy (what a consumer
// allows), and the parts of the request that were refused. A request never grants anything by
// itself: everything in the grant is allowed by the policy. To grant a policy as it stands,
// pass it as the request too.
//
// The rules, per class (testdata/v1 "grant"):
//   - filesystem: each requested path gets the requested access that some policy path at or
//     above it allows; the rest is refused, and a path with no access left is dropped;
//   - network: a requested mode is granted when the policy has the same mode (ports and hosts
//     are intersected) or an open network; anything else is refused and the grant reaches
//     nothing;
//   - process: children only when both allow them;
//   - devices: the requested devices the policy lists;
//   - resources: the lower of the two where both set a bound, else whichever is set; a request
//     above the policy's bound is refused and the policy's bound is granted;
//   - environment: the entries and secrets the policy supplies for the requested names (a
//     request's names, and the names of its own entries and secrets, whose values are never
//     used); a name the policy does not supply is refused;
//   - lifecycle: the policy's, which must name its workspace or snapshot; a requested mode or
//     restore scope that differs is refused.
//
// The grant states the network and lifecycle modes explicitly. Grant returns ErrInvalid when
// either input fails Validate, or when the policy's workspace or snapshot has no name.
func Grant(request, policy Policy) (Policy, []Refusal, error) {
	if request.Validate() != nil || policy.Validate() != nil {
		return Policy{}, nil, ErrInvalid
	}
	if (policy.Lifecycle.Mode == LifecycleWorkspace || policy.Lifecycle.Mode == LifecycleSnapshot) && policy.Lifecycle.Name == "" {
		return Policy{}, nil, ErrInvalid
	}
	var grant Policy
	var refused []Refusal

	for _, want := range request.Filesystem {
		allowed := map[Access]bool{}
		for _, have := range policy.Filesystem {
			if within(want.Path, have.Path) {
				for _, a := range have.Access {
					allowed[a] = true
				}
			}
		}
		var granted, missing []Access
		for _, a := range accessOrder {
			if !contains(want.Access, a) {
				continue
			}
			if allowed[a] {
				granted = append(granted, a)
			} else {
				missing = append(missing, a)
			}
		}
		if len(granted) > 0 {
			grant.Filesystem = append(grant.Filesystem, Path{Path: want.Path, Access: granted})
		}
		if len(missing) > 0 {
			refused = append(refused, Refusal{Class: "filesystem", Path: want.Path, Access: missing})
		}
	}

	grant.Network, refused = grantNetwork(request.Network, policy.Network, refused)

	if request.Process.Children {
		if policy.Process.Children {
			grant.Process.Children = true
		} else {
			refused = append(refused, Refusal{Class: "process"})
		}
	}

	for _, device := range request.Devices {
		if contains(policy.Devices, device) {
			grant.Devices = append(grant.Devices, device)
		} else {
			refused = append(refused, Refusal{Class: "devices", Name: device})
		}
	}

	policyResources := policy.Resources.fields()
	for i, want := range request.Resources.fields() {
		have := policyResources[i]
		switch {
		case want.v == nil && have.v == nil:
		case want.v == nil:
			grant.Resources.set(have.name, value(*have.v))
		case have.v == nil || *want.v <= *have.v:
			grant.Resources.set(want.name, value(*want.v))
		default:
			grant.Resources.set(have.name, value(*have.v))
			refused = append(refused, Refusal{Class: "resources." + want.name})
		}
	}

	for _, name := range request.Environment.requested() {
		found := false
		for _, entry := range policy.Environment.Entries {
			if envName(entry) == name {
				grant.Environment.Entries = append(grant.Environment.Entries, entry)
				found = true
			}
		}
		for _, secret := range policy.Environment.Secrets {
			if secret.Name == name {
				grant.Environment.Secrets = append(grant.Environment.Secrets, secret)
				found = true
			}
		}
		if !found {
			refused = append(refused, Refusal{Class: "environment", Name: name})
		}
	}

	grant.Lifecycle = policy.Lifecycle
	if grant.Lifecycle.Mode == "" {
		grant.Lifecycle.Mode = LifecycleFresh
	}
	if want := request.Lifecycle; want.Mode != "" {
		if want.Mode != grant.Lifecycle.Mode || (want.Mode == LifecycleSnapshot && want.Restore != grant.Lifecycle.Restore) {
			refused = append(refused, Refusal{Class: "lifecycle"})
		}
	}
	return grant, refused, nil
}

func grantNetwork(want, have Network, refused []Refusal) (Network, []Refusal) {
	none := Network{Mode: NetworkNone}
	wantMode, haveMode := want.Mode, have.Mode
	if wantMode == "" {
		wantMode = NetworkNone
	}
	if haveMode == "" {
		haveMode = NetworkNone
	}
	switch {
	case wantMode == NetworkNone:
		return none, refused
	case haveMode == NetworkOpen:
		want.Mode = wantMode
		return want, refused
	case wantMode != haveMode:
		return none, append(refused, Refusal{Class: "network"})
	case wantMode == NetworkPorts:
		granted := Network{Mode: NetworkPorts}
		for _, port := range want.Ports {
			if containsInt(have.Ports, port) {
				granted.Ports = append(granted.Ports, port)
			} else {
				refused = append(refused, Refusal{Class: "network", Port: port})
			}
		}
		if len(granted.Ports) == 0 {
			return none, refused
		}
		return granted, refused
	case wantMode == NetworkProxy:
		granted := Network{Mode: NetworkProxy}
		for _, host := range want.Hosts {
			if containsHost(have.Hosts, host) {
				granted.Hosts = append(granted.Hosts, host)
			} else {
				h := host
				refused = append(refused, Refusal{Class: "network", Host: &h})
			}
		}
		if len(granted.Hosts) == 0 {
			return none, refused
		}
		return granted, refused
	}
	// open requested, open granted
	return Network{Mode: NetworkOpen}, refused
}

// requested returns the names a request asks for: its Names, then the names of its entries and
// secrets, without repeats. The values a request gives are never granted; only the policy's are.
func (e Environment) requested() []string {
	var names []string
	add := func(name string) {
		if !contains(names, name) {
			names = append(names, name)
		}
	}
	for _, name := range e.Names {
		add(name)
	}
	for _, entry := range e.Entries {
		add(envName(entry))
	}
	for _, secret := range e.Secrets {
		add(secret.Name)
	}
	return names
}

func value(v int64) *int64 { return &v }

func envName(entry string) string {
	for i := 0; i < len(entry); i++ {
		if entry[i] == '=' {
			return entry[:i]
		}
	}
	return entry
}

func contains[T comparable](list []T, item T) bool {
	for _, x := range list {
		if x == item {
			return true
		}
	}
	return false
}

func containsInt(list []int, item int) bool { return contains(list, item) }

func containsHost(list []Host, item Host) bool { return contains(list, item) }
