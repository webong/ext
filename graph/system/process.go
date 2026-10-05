package systemgraph

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"syscall"
	"time"
)

const processCommandWait = 250 * time.Millisecond

// CollectionStatus distinguishes an empty observation from inaccessible data.
// State is complete, partial, permission-denied, unsupported, exited, or unavailable.
// Detail is a diagnostic, never process arguments or environment contents.
type CollectionStatus struct {
	State      string     `json:"state"`
	Detail     string     `json:"detail,omitempty"`
	ObservedAt *time.Time `json:"observed_at,omitempty"`
}

// ProcessOptions bounds returned records and collection time. Zero values select
// 4096 processes, 1024 resources per category, and a ten-second deadline.
// Detailed resource inspection is opt-in through InspectProcess.
type ProcessOptions struct {
	MaxProcesses int
	MaxResources int
	Timeout      time.Duration
}

type ProcessUsage struct {
	ResidentBytes *uint64  `json:"resident_bytes,omitempty"`
	VirtualBytes  *uint64  `json:"virtual_bytes,omitempty"`
	CPUSeconds    *float64 `json:"cpu_seconds,omitempty"`
	Threads       *uint32  `json:"threads,omitempty"`
}

// ApplicationInfo identifies an application through OS packaging conventions.
// It does not assign product capabilities or infer an engine from a name.
type ApplicationInfo struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// ProcessResource is OS evidence. A socket does not imply an automation API.
// Endpoint preserves an OS-rendered address when its roles cannot be established.
// Descriptors are process-local observations, not reusable handles.
type ProcessResource struct {
	Kind          string `json:"kind"`
	Path          string `json:"path,omitempty"`
	Descriptor    string `json:"descriptor,omitempty"`
	Protocol      string `json:"protocol,omitempty"`
	LocalAddress  string `json:"local_address,omitempty"`
	RemoteAddress string `json:"remote_address,omitempty"`
	Endpoint      string `json:"endpoint,omitempty"`
	State         string `json:"state,omitempty"`
}

// ProcessInfo identifies a process instance using PID and an OS start identity.
// StartID is opaque and platform-specific. Empty StartID means identity could not
// be established; such entries are returned but never projected as stable nodes.
type ProcessInfo struct {
	PID            int                         `json:"pid"`
	ParentPID      int                         `json:"parent_pid"`
	Name           string                      `json:"name"`
	StartID        string                      `json:"start_id,omitempty"`
	Executable     string                      `json:"executable,omitempty"`
	Owner          string                      `json:"owner,omitempty"`
	Application    *ApplicationInfo            `json:"application,omitempty"`
	Usage          *ProcessUsage               `json:"usage,omitempty"`
	Coverage       map[string]CollectionStatus `json:"coverage"`
	Resources      []ProcessResource           `json:"resources,omitempty"`
	resourceCounts map[string]int
}

// ProcessSnapshot is a non-atomic, point-in-time OS observation. Scope is all or
// process; only complete all-process enumeration permits removing absent PIDs.
// HostID includes the boot/namespace identity so records cannot cross reboots.
type ProcessSnapshot struct {
	HostID      string           `json:"host_id"`
	Scope       string           `json:"scope"`
	ObservedAt  time.Time        `json:"observed_at"`
	Enumeration CollectionStatus `json:"enumeration"`
	Processes   []ProcessInfo    `json:"processes"`
}

func processOptions(options ProcessOptions) (ProcessOptions, error) {
	if options.MaxProcesses < 0 || options.MaxResources < 0 || options.Timeout < 0 {
		return options, errors.New("process limits and timeout must not be negative")
	}
	if options.MaxProcesses == 0 {
		options.MaxProcesses = 4096
	}
	if options.MaxResources == 0 {
		options.MaxResources = 1024
	}
	if options.Timeout == 0 {
		options.Timeout = 10 * time.Second
	}
	if options.MaxProcesses > 65536 || options.MaxResources > 65536 {
		return options, errors.New("process and resource limits must not exceed 65536")
	}
	return options, nil
}

// DiscoverProcesses collects identities without scanning every process's files.
// Processes hidden by the OS are outside this caller-visible inventory.
func DiscoverProcesses(ctx context.Context, options ProcessOptions) (ProcessSnapshot, error) {
	options, err := processOptions(options)
	if err != nil {
		return ProcessSnapshot{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, options.Timeout)
	defer cancel()
	host, err := processHostID(ctx)
	if err != nil {
		return ProcessSnapshot{}, err
	}
	result := ProcessSnapshot{HostID: host, Scope: "all", ObservedAt: time.Now().UTC(), Processes: []ProcessInfo{}}
	result.Processes, result.Enumeration, err = platformProcesses(ctx, options.MaxProcesses)
	if err != nil {
		return result, err
	}
	if err = ctx.Err(); err != nil {
		return result, err
	}
	stampProcessSnapshot(&result)
	sort.Slice(result.Processes, func(i, j int) bool { return result.Processes[i].PID < result.Processes[j].PID })
	return result, nil
}

// InspectProcess inspects one process and checks its identity again afterwards.
// Resources collected during exit or PID reuse are discarded, never reassigned.
func InspectProcess(ctx context.Context, pid int, options ProcessOptions) (ProcessSnapshot, error) {
	if pid <= 0 || uint64(pid) > uint64(1<<31-1) {
		return ProcessSnapshot{}, errors.New("process PID must be a positive 32-bit integer")
	}
	options, err := processOptions(options)
	if err != nil {
		return ProcessSnapshot{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, options.Timeout)
	defer cancel()
	host, err := processHostID(ctx)
	if err != nil {
		return ProcessSnapshot{}, err
	}
	before, err := platformProcess(ctx, pid)
	if err != nil {
		return ProcessSnapshot{}, fmt.Errorf("inspect process %d: %w", pid, err)
	}
	if before.StartID == "" {
		return ProcessSnapshot{}, fmt.Errorf("cannot establish identity for process %d", pid)
	}
	platformProcessResources(ctx, &before, options.MaxResources)
	if err := ctx.Err(); err != nil {
		return ProcessSnapshot{}, err
	}
	after, err := platformProcess(ctx, pid)
	if err != nil {
		return ProcessSnapshot{}, fmt.Errorf("recheck process %d: %w", pid, err)
	}
	if err := ctx.Err(); err != nil {
		return ProcessSnapshot{}, err
	}
	if after.StartID != before.StartID {
		return ProcessSnapshot{}, errors.New("process exited or PID was reused during inspection")
	}
	if before.Executable != after.Executable {
		return ProcessSnapshot{}, errors.New("process executable changed during inspection")
	}
	sort.Slice(before.Resources, func(i, j int) bool {
		a, b := before.Resources[i], before.Resources[j]
		return a.Kind+"\x00"+a.Descriptor+"\x00"+a.Path+"\x00"+a.LocalAddress < b.Kind+"\x00"+b.Descriptor+"\x00"+b.Path+"\x00"+b.LocalAddress
	})
	result := ProcessSnapshot{HostID: host, Scope: "process", ObservedAt: time.Now().UTC(), Enumeration: completeProcessStatus(), Processes: []ProcessInfo{before}}
	stampProcessSnapshot(&result)
	return result, nil
}

func completeProcessStatus() CollectionStatus { return CollectionStatus{State: "complete"} }
func processStatus(err error) CollectionStatus {
	if err == nil {
		return completeProcessStatus()
	}
	state := "unavailable"
	if errors.Is(err, os.ErrPermission) {
		state = "permission-denied"
	}
	if errors.Is(err, syscall.ESRCH) {
		state = "exited"
	}
	return CollectionStatus{State: state, Detail: err.Error()}
}
func limitedProcessStatus() CollectionStatus {
	return CollectionStatus{State: "partial", Detail: "record limit reached"}
}
func unsupportedProcessStatus(detail string) CollectionStatus {
	return CollectionStatus{State: "unsupported", Detail: detail}
}
func newProcessInfo(pid int) ProcessInfo {
	return ProcessInfo{PID: pid, Coverage: map[string]CollectionStatus{}}
}
func processInstanceID(host string, p ProcessInfo) string {
	return "host-process/" + digest(host+"\x00"+strconv.Itoa(p.PID)+"\x00"+p.StartID)
}

func addProcessResource(p *ProcessInfo, resource ProcessResource, limit int) {
	if p.resourceCounts == nil {
		p.resourceCounts = make(map[string]int)
		for _, r := range p.Resources {
			p.resourceCounts[r.Kind]++
		}
	}
	for _, value := range []string{resource.Path, resource.Endpoint, resource.LocalAddress, resource.RemoteAddress, resource.Protocol, resource.Descriptor, resource.State} {
		if len(value) > 4096 {
			p.Coverage[resource.Kind] = CollectionStatus{State: "partial", Detail: "resource exceeds graph string limit"}
			return
		}
	}
	if p.resourceCounts[resource.Kind] >= limit {
		p.Coverage[resource.Kind] = limitedProcessStatus()
		return
	}
	p.Resources = append(p.Resources, resource)
	p.resourceCounts[resource.Kind]++
}

func stampProcessSnapshot(snapshot *ProcessSnapshot) {
	at := snapshot.ObservedAt
	snapshot.Enumeration.ObservedAt = &at
	for i := range snapshot.Processes {
		for category, status := range snapshot.Processes[i].Coverage {
			if snapshot.Processes[i].resourceCounts[category] > 0 && (status.State == "unavailable" || status.State == "permission-denied" || status.State == "exited") {
				status.Detail = status.State + ": " + status.Detail
				status.State = "partial"
			}
			status.ObservedAt = &at
			snapshot.Processes[i].Coverage[category] = status
		}
	}
}
