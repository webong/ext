//go:build linux && !amd64 && !arm64

package confine

// seccompArch 0 means the filter is not built for this architecture: the driver reports
// network.none and process as unsupported here.
const (
	seccompArch = 0
	seccompX32  = false
)

var (
	forkCalls    []uint32
	archHighRisk []uint32
)
