// Package network observes the local network stack. It does not dial, listen,
// resolve names, change routes, configure interfaces, or contact remote hosts.
package network

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/webong/ext/res/system/internal/capability"
)

// ErrInvalid reports a malformed option, status or address.
var ErrInvalid = errors.New("invalid network value")

// Code is the stable error code of err for another language to match: invalid. It is empty for an
// error this package does not define.
func Code(err error) string {
	if errors.Is(err, ErrInvalid) {
		return "invalid"
	}
	return ""
}

// CollectionStatus distinguishes empty OS data from unavailable or unsupported
// collection. State is complete, partial, permission-denied, unsupported, or
// unavailable. Reason says why something is missing, from the same set in every package
// (testdata/capability/v1): unsupported on this platform, permission, no_device or tool_missing.
type CollectionStatus struct {
	State      string     `json:"state"`
	Reason     string     `json:"reason,omitempty"`
	Detail     string     `json:"detail,omitempty"`
	ObservedAt *time.Time `json:"observed_at,omitempty"`
}

// Validate checks that the state is known and the reason fits it: none for complete and partial,
// permission or none for permission-denied, unsupported or tool_missing for unsupported, and none,
// permission, no_device or tool_missing for unavailable (a failed native read has no reason).
func (s CollectionStatus) Validate() error {
	if !capability.Allowed(s.State, s.Reason) || s.State == "exited" {
		return fmt.Errorf("%w: state %q with reason %q", ErrInvalid, s.State, s.Reason)
	}
	return nil
}

// Address is an address assigned to a local interface.
type Address struct {
	IP           string `json:"ip"`
	PrefixLength int    `json:"prefix_length"`
	Family       string `json:"family"`
	Scope        string `json:"scope"`
}

// Interface is an OS network interface visible to the current process.
type Interface struct {
	Index           int       `json:"index"`
	Name            string    `json:"name"`
	MTU             int       `json:"mtu"`
	HardwareAddress string    `json:"hardware_address,omitempty"`
	Flags           []string  `json:"flags"`
	Addresses       []Address `json:"addresses"`
}

// Route is an entry in the local route table. Destination is CIDR. An empty
// gateway means an on-link route. Interface names are local OS identities.
type Route struct {
	Destination string `json:"destination"`
	Gateway     string `json:"gateway,omitempty"`
	Interface   string `json:"interface,omitempty"`
	Metric      int    `json:"metric,omitempty"`
	Default     bool   `json:"default"`
}

// Resolver describes the local resolver configuration without performing a
// lookup. Servers never include credentials.
type Resolver struct {
	Servers []string `json:"servers"`
	Search  []string `json:"search"`
	Options []string `json:"options"`
}

// Snapshot is a bounded, non-atomic observation of local network state.
type Snapshot struct {
	Platform        string           `json:"platform"`
	ObservedAt      time.Time        `json:"observed_at"`
	Interfaces      []Interface      `json:"interfaces"`
	Routes          []Route          `json:"routes"`
	Resolver        Resolver         `json:"resolver"`
	InterfaceStatus CollectionStatus `json:"interface_status"`
	RouteStatus     CollectionStatus `json:"route_status"`
	ResolverStatus  CollectionStatus `json:"resolver_status"`
}

// Options bounds records and collection time. Every field is explicit and a
// zero is invalid, as in the other bindings (testdata/network/v1); start from
// DefaultOptions (256 interfaces, 4096 routes, 256 addresses per interface,
// five seconds) and change what differs.
type Options struct {
	MaxInterfaces int
	MaxAddresses  int
	MaxRoutes     int
	Timeout       time.Duration
}

// DefaultOptions returns the default of every limit and the default timeout.
func DefaultOptions() Options {
	return Options{MaxInterfaces: 256, MaxAddresses: 256, MaxRoutes: 4096, Timeout: 5 * time.Second}
}

// Validate checks that every limit is between 1 and 65536 and that the
// timeout is positive.
func (o Options) Validate() error {
	for _, limit := range []int{o.MaxInterfaces, o.MaxAddresses, o.MaxRoutes} {
		if limit < 1 || limit > 65536 {
			return fmt.Errorf("%w: network limits must be between 1 and 65536", ErrInvalid)
		}
	}
	if o.Timeout <= 0 {
		return fmt.Errorf("%w: network timeout must be positive", ErrInvalid)
	}
	return nil
}

func parseAddress(value string) (Address, bool) {
	ip, network, err := net.ParseCIDR(value)
	if err != nil {
		return Address{}, false
	}
	prefix, _ := network.Mask.Size()
	family := "ipv6"
	if ip.To4() != nil {
		family = "ipv4"
	}
	scope := "global"
	switch {
	case ip.IsLoopback():
		scope = "loopback"
	case ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast():
		scope = "link"
	case ip.IsPrivate():
		scope = "private"
	case ip.IsUnspecified():
		scope = "unspecified"
	case ip.IsMulticast():
		scope = "multicast"
	}
	return Address{IP: ip.String(), PrefixLength: prefix, Family: family, Scope: scope}, true
}

// cleanResolver trims each entry (leading and trailing U+0009 to U+000D and U+0020) and drops empty
// and repeated entries, keeping the first of each and the order the system reported, which is
// meaningful for name servers and search domains: the rule of every binding (testdata/network/v1).
func cleanResolver(values []string) []string {
	result := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimFunc(value, func(character rune) bool { return character == ' ' || character >= '\t' && character <= '\r' })
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}
