// Package network observes the local network stack. It does not dial, listen,
// resolve names, change routes, configure interfaces, or contact remote hosts.
package network

import (
	"context"
	"errors"
	"fmt"
	"net"
	"runtime"
	"sort"
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

// Discover observes local network interfaces, routes, and resolver settings.
// It performs no DNS lookup and opens no network connection.
func Discover(ctx context.Context, options Options) (Snapshot, error) {
	err := options.Validate()
	if err != nil {
		return Snapshot{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, options.Timeout)
	defer cancel()
	now := time.Now().UTC()
	result := Snapshot{Platform: runtime.GOOS, ObservedAt: now, Interfaces: []Interface{}, Routes: []Route{}, Resolver: Resolver{Servers: []string{}, Search: []string{}, Options: []string{}}}
	result.Interfaces, result.InterfaceStatus, err = discoverInterfaces(ctx, options)
	if err != nil {
		return result, err
	}
	result.Routes, result.RouteStatus, err = platformRoutes(ctx, options.MaxRoutes)
	if err != nil {
		return result, err
	}
	result.Resolver, result.ResolverStatus, err = platformResolver(ctx)
	if err != nil {
		return result, err
	}
	stamp(&result.InterfaceStatus, now)
	stamp(&result.RouteStatus, now)
	stamp(&result.ResolverStatus, now)
	sort.Slice(result.Interfaces, func(i, j int) bool { return result.Interfaces[i].Index < result.Interfaces[j].Index })
	sort.Slice(result.Routes, func(i, j int) bool {
		a, b := result.Routes[i], result.Routes[j]
		return a.Destination+"\x00"+a.Gateway+"\x00"+a.Interface < b.Destination+"\x00"+b.Gateway+"\x00"+b.Interface
	})
	return result, ctx.Err()
}

func discoverInterfaces(ctx context.Context, options Options) ([]Interface, CollectionStatus, error) {
	values, err := net.Interfaces()
	if err != nil {
		return []Interface{}, unavailable(err, "network interface enumeration failed"), nil
	}
	result := make([]Interface, 0, min(len(values), options.MaxInterfaces))
	partial := len(values) > options.MaxInterfaces
	for _, value := range values {
		if err := ctx.Err(); err != nil {
			return nil, CollectionStatus{}, err
		}
		if len(result) >= options.MaxInterfaces {
			break
		}
		item := Interface{Index: value.Index, Name: value.Name, MTU: value.MTU, HardwareAddress: value.HardwareAddr.String(), Flags: interfaceFlags(value.Flags), Addresses: []Address{}}
		addresses, addressErr := value.Addrs()
		if addressErr != nil {
			partial = true
		} else {
			if len(addresses) > options.MaxAddresses {
				partial = true
				addresses = addresses[:options.MaxAddresses]
			}
			for _, raw := range addresses {
				address, ok := parseAddress(raw.String())
				if ok {
					item.Addresses = append(item.Addresses, address)
				}
			}
			sort.Slice(item.Addresses, func(i, j int) bool { return item.Addresses[i].IP < item.Addresses[j].IP })
		}
		result = append(result, item)
	}
	if partial {
		return result, status("partial", "interface or address limit reached, or an interface could not be inspected"), nil
	}
	return result, status("complete", ""), nil
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

func interfaceFlags(flags net.Flags) []string {
	result := []string{}
	for bit, name := range map[net.Flags]string{net.FlagUp: "up", net.FlagBroadcast: "broadcast", net.FlagLoopback: "loopback", net.FlagPointToPoint: "point-to-point", net.FlagMulticast: "multicast", net.FlagRunning: "running"} {
		if flags&bit != 0 {
			result = append(result, name)
		}
	}
	return result
}

func status(state, detail string) CollectionStatus {
	return CollectionStatus{State: state, Detail: detail}
}

// unavailable reports something this platform can observe but could not now. The reason comes
// from err, the failure of the native read: permission, tool_missing, or no_device when the source
// to read is absent or did not answer (a nil err).
func unavailable(err error, detail string) CollectionStatus {
	return CollectionStatus{State: "unavailable", Reason: capability.Unavailable(err), Detail: detail}
}

// unsupported reports a platform that cannot observe something at all.
func unsupported(detail string) CollectionStatus {
	return CollectionStatus{State: "unsupported", Reason: "unsupported", Detail: detail}
}
func stamp(value *CollectionStatus, observed time.Time) {
	if value.State == "" {
		value.State = "complete"
	}
	if value.ObservedAt == nil {
		timeValue := observed
		value.ObservedAt = &timeValue
	}
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
