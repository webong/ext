package sandbox

import (
	"fmt"
	"strings"
	"testing"
)

// The bounds are numbers in testdata/v1 "limits"; these cases build inputs at and past each one,
// which would be too large to write out as fixtures.
func TestBounds(t *testing.T) {
	paths := func(n int) []Path {
		out := make([]Path, n)
		for i := range out {
			out[i] = Path{Path: fmt.Sprintf("/p%d", i), Access: []Access{Read}}
		}
		return out
	}
	ports := func(n int) []int {
		out := make([]int, n)
		for i := range out {
			out[i] = i + 1
		}
		return out
	}
	hosts := func(n int) []Host {
		out := make([]Host, n)
		for i := range out {
			out[i] = Host{Host: fmt.Sprintf("h%d.example", i), Port: 443}
		}
		return out
	}
	devices := func(n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = fmt.Sprintf("d%d", i)
		}
		return out
	}
	names := func(n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = fmt.Sprintf("N%d", i)
		}
		return out
	}
	longPath := "/" + strings.Repeat("a", MaxPathBytes-1)
	label := strings.Repeat("a", 63)
	longHost := strings.Repeat(label+".", 3) + strings.Repeat("a", MaxHostBytes-3*64)
	cases := []struct {
		name string
		p    Policy
		ok   bool
	}{
		{"paths-at-bound", Policy{Filesystem: paths(MaxPaths)}, true},
		{"paths-past-bound", Policy{Filesystem: paths(MaxPaths + 1)}, false},
		{"path-at-bound", Policy{Filesystem: []Path{{Path: longPath, Access: []Access{Read}}}}, true},
		{"path-past-bound", Policy{Filesystem: []Path{{Path: longPath + "a", Access: []Access{Read}}}}, false},
		{"ports-at-bound", Policy{Network: Network{Mode: NetworkPorts, Ports: ports(MaxPorts)}}, true},
		{"ports-past-bound", Policy{Network: Network{Mode: NetworkPorts, Ports: ports(MaxPorts + 1)}}, false},
		{"hosts-at-bound", Policy{Network: Network{Mode: NetworkProxy, Hosts: hosts(MaxHosts)}}, true},
		{"hosts-past-bound", Policy{Network: Network{Mode: NetworkProxy, Hosts: hosts(MaxHosts + 1)}}, false},
		{"host-at-bound", Policy{Network: Network{Mode: NetworkProxy, Hosts: []Host{{Host: longHost, Port: 1}}}}, true},
		{"host-past-bound", Policy{Network: Network{Mode: NetworkProxy, Hosts: []Host{{Host: longHost + "a", Port: 1}}}}, false},
		{"label-past-63", Policy{Network: Network{Mode: NetworkProxy, Hosts: []Host{{Host: label + "a.com", Port: 1}}}}, false},
		{"devices-at-bound", Policy{Devices: devices(MaxDevices)}, true},
		{"devices-past-bound", Policy{Devices: devices(MaxDevices + 1)}, false},
		{"device-name-past-bound", Policy{Devices: []string{strings.Repeat("d", MaxDeviceBytes+1)}}, false},
		{"env-names-at-bound", Policy{Environment: Environment{Names: names(MaxEnvNames)}}, true},
		{"env-names-past-bound", Policy{Environment: Environment{Names: names(MaxEnvNames + 1)}}, false},
		{"env-entry-at-bound", Policy{Environment: Environment{Entries: []string{"A=" + strings.Repeat("v", MaxEnvBytes-2)}}}, true},
		{"env-entry-past-bound", Policy{Environment: Environment{Entries: []string{"A=" + strings.Repeat("v", MaxEnvBytes-1)}}}, false},
		{"secret-ref-past-bound", Policy{Environment: Environment{Secrets: []Secret{{Name: "A", Ref: strings.Repeat("r", MaxRefBytes+1)}}}}, false},
		{"lifecycle-name-past-bound", Policy{Lifecycle: Lifecycle{Mode: LifecycleWorkspace, Name: strings.Repeat("n", MaxNameBytes+1)}}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := c.p.Validate(); (err == nil) != c.ok {
				t.Errorf("Validate() = %v, want ok %v", err, c.ok)
			}
		})
	}
}
