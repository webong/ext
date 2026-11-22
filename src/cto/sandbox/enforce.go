package sandbox

// The classes a runner reports it can enforce. A grant needs a fixed set of them (Required):
// denying the filesystem, process creation and devices, and scrubbing the environment, always;
// the class of its network mode; the class of its lifecycle; and one class per resource bound.
var Classes = []string{
	"filesystem",
	"network.none",
	"network.ports",
	"network.proxy",
	"network.open",
	"process",
	"devices",
	"environment",
	"resources.memoryBytes",
	"resources.milliCpu",
	"resources.pids",
	"resources.cpuTimeMs",
	"resources.wallTimeMs",
	"resources.openFiles",
	"resources.writeBytes",
	"lifecycle.fresh",
	"lifecycle.workspace",
	"lifecycle.snapshot.filesystem",
	"lifecycle.snapshot.memory",
}

// The reasons a class is not enforced (docs/adr-architecture.md, rule 2).
const (
	ReasonUnsupported = "unsupported"
	ReasonPermission  = "permission"
	ReasonToolMissing = "tool_missing"
)

func knownClass(class string) bool { return contains(Classes, class) }

// Required returns the classes a grant needs enforced, in the order of Classes. It returns
// ErrInvalid when grant fails Validate.
func Required(grant Policy) ([]string, error) {
	if grant.Validate() != nil {
		return nil, ErrInvalid
	}
	need := map[string]bool{"filesystem": true, "process": true, "devices": true, "environment": true}
	mode := grant.Network.Mode
	if mode == "" {
		mode = NetworkNone
	}
	need["network."+mode] = true
	for _, field := range grant.Resources.fields() {
		if field.v != nil {
			need["resources."+field.name] = true
		}
	}
	switch grant.Lifecycle.Mode {
	case "", LifecycleFresh:
		need["lifecycle.fresh"] = true
	case LifecycleWorkspace:
		need["lifecycle.workspace"] = true
	case LifecycleSnapshot:
		need["lifecycle.snapshot."+grant.Lifecycle.Restore] = true
	}
	var out []string
	for _, class := range Classes {
		if need[class] {
			out = append(out, class)
		}
	}
	return out, nil
}

// Support is whether a runner enforces one class here, and why not.
type Support struct {
	Enforced bool   `json:"enforced"`
	Reason   string `json:"reason,omitempty"`
}

// Capabilities is what a runner (one driver on this host) can enforce. A class that is not
// listed is not enforced, for the reason "unsupported".
type Capabilities struct {
	Boundary Boundary           `json:"boundary"`
	Driver   string             `json:"driver,omitempty"`
	Classes  map[string]Support `json:"classes,omitempty"`
}

// Validate checks that the boundary is a level, every class is known, an enforced class has no
// reason and a class that is not enforced has one of the reasons.
func (c Capabilities) Validate() error {
	if !c.Boundary.Valid() || len(c.Driver) > MaxNameBytes || (c.Driver != "" && !validName(c.Driver)) {
		return ErrInvalid
	}
	for class, support := range c.Classes {
		if !knownClass(class) {
			return ErrInvalid
		}
		if support.Enforced && support.Reason != "" {
			return ErrInvalid
		}
		if !support.Enforced && support.Reason != ReasonUnsupported && support.Reason != ReasonPermission && support.Reason != ReasonToolMissing {
			return ErrInvalid
		}
	}
	return nil
}

// Missing is one required class a runner does not enforce, and why.
type Missing struct {
	Class  string `json:"class"`
	Reason string `json:"reason"`
}

// Outcome is what Enforce decided: the required classes the runner enforces, the ones it does
// not, and of those, the ones the caller accepted unenforced.
type Outcome struct {
	Enforced   []string  `json:"enforced"`
	Missing    []Missing `json:"missing,omitempty"`
	Unenforced []string  `json:"unenforced,omitempty"`
}

// Enforce checks a grant against a runner's capabilities. Every class the grant needs must be
// enforced, or be named in accept, the classes the caller agrees to run without. It fails closed:
// when a required class is neither, it returns ErrUnsupported with the outcome, whose Missing
// lists every such class, and the launch must not happen. Classes in accept that the grant does
// not need are ignored. Enforce returns ErrInvalid for an invalid grant, capability set or
// unknown class in accept.
func Enforce(grant Policy, caps Capabilities, accept []string) (Outcome, error) {
	required, err := Required(grant)
	if err != nil || caps.Validate() != nil {
		return Outcome{}, ErrInvalid
	}
	for _, class := range accept {
		if !knownClass(class) {
			return Outcome{}, ErrInvalid
		}
	}
	out := Outcome{Enforced: []string{}}
	failed := false
	for _, class := range required {
		support, listed := caps.Classes[class]
		if listed && support.Enforced {
			out.Enforced = append(out.Enforced, class)
			continue
		}
		reason := ReasonUnsupported
		if listed {
			reason = support.Reason
		}
		out.Missing = append(out.Missing, Missing{Class: class, Reason: reason})
		if contains(accept, class) {
			out.Unenforced = append(out.Unenforced, class)
		} else {
			failed = true
		}
	}
	if failed {
		return out, ErrUnsupported
	}
	return out, nil
}

// Report is what every launch returns and what a consumer records: where it ran, what was
// requested, granted and refused, and what was actually enforced.
type Report struct {
	Boundary   Boundary  `json:"boundary"`
	Driver     string    `json:"driver,omitempty"`
	Grant      Policy    `json:"grant"`
	Refused    []Refusal `json:"refused,omitempty"`
	Enforced   []string  `json:"enforced"`
	Unenforced []string  `json:"unenforced,omitempty"`
}

// Validate checks that the report is well formed: a level, a valid grant, known classes, every
// required class either enforced or unenforced and none both.
func (r Report) Validate() error {
	if !r.Boundary.Valid() || (r.Driver != "" && !validName(r.Driver)) {
		return ErrInvalid
	}
	required, err := Required(r.Grant)
	if err != nil {
		return ErrInvalid
	}
	seen := map[string]bool{}
	for _, class := range append(append([]string{}, r.Enforced...), r.Unenforced...) {
		if !knownClass(class) || seen[class] {
			return ErrInvalid
		}
		seen[class] = true
	}
	for _, class := range required {
		if !seen[class] {
			return ErrInvalid
		}
	}
	for _, refusal := range r.Refused {
		if !knownRefusalClass(refusal.Class) {
			return ErrInvalid
		}
	}
	return nil
}

func knownRefusalClass(class string) bool {
	switch class {
	case "filesystem", "network", "process", "devices", "environment", "lifecycle":
		return true
	}
	for _, field := range (Resources{}).fields() {
		if class == "resources."+field.name {
			return true
		}
	}
	return false
}

// Sandboxed reports whether a launch meets the evaluator isolation "sandboxed"
// (docs/evaluator-adapters.md): its boundary is confined, runtime, container or kernel, every
// required class was enforced, and its network is not open. A report that fails Validate is
// never sandboxed.
func Sandboxed(r Report) bool {
	if r.Validate() != nil {
		return false
	}
	switch r.Boundary {
	case Confined, Runtime, Container, Kernel:
	default:
		return false
	}
	return len(r.Unenforced) == 0 && r.Grant.Network.Mode != NetworkOpen
}

// Plan is the whole decision before a launch: accept the boundary, compute the grant and check
// it against the runner. It returns the report to hand to the consumer, or the first error:
// ErrInvalid, ErrDenied for a boundary outside accepted, or ErrUnsupported (with the report's
// grant and refusals filled in) for a class the runner cannot enforce and the caller did not
// accept.
func Plan(request, policy Policy, accepted []Boundary, caps Capabilities, acceptUnenforced []string) (Report, error) {
	if err := caps.Validate(); err != nil {
		return Report{}, err
	}
	if err := Accept(accepted, caps.Boundary); err != nil {
		return Report{}, err
	}
	grant, refused, err := Grant(request, policy)
	if err != nil {
		return Report{}, err
	}
	report := Report{Boundary: caps.Boundary, Driver: caps.Driver, Grant: grant, Refused: refused}
	outcome, err := Enforce(grant, caps, acceptUnenforced)
	report.Enforced = outcome.Enforced
	report.Unenforced = outcome.Unenforced
	return report, err
}
