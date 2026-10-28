package system

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ResourceInfo is a point-in-time observation of the local machine's computing
// resources. Container-adjusted values never guess: a field whose native source
// is unavailable carries a non-complete status instead of the host value.
//
// It is local host state only. Network shape stays in res/system/network and is
// never duplicated here.
type ResourceInfo struct {
	OS                  string           `json:"os"`
	Architecture        string           `json:"architecture"`
	ObservedAt          time.Time        `json:"observed_at"`
	LogicalCPUs         int              `json:"logical_cpus"`
	CPUCapacity         float64          `json:"cpu_capacity"`
	CPUStatus           CollectionStatus `json:"cpu_status"`
	MemoryBytes         uint64           `json:"memory_bytes"`
	MemoryCapacityBytes uint64           `json:"memory_capacity_bytes"`
	MemoryStatus        CollectionStatus `json:"memory_status"`
	Storage             []StorageInfo    `json:"storage,omitempty"`
	Power               *PowerInfo       `json:"power,omitempty"`
	Container           ContainerInfo    `json:"container"`
	Uptime              *time.Duration   `json:"uptime,omitempty"`
	BootTime            *time.Time       `json:"boot_time,omitempty"`
	UptimeStatus        CollectionStatus `json:"uptime_status"`
}

// ContainerInfo reports whether this process runs inside a container and which
// local markers support that conclusion. Evidence is a sorted list of stable
// names, not file contents or environment values.
type ContainerInfo struct {
	Detected bool     `json:"detected"`
	State    string   `json:"state"`
	Evidence []string `json:"evidence"`
}

// StorageInfo describes one mounted storage volume visible to the process.
type StorageInfo struct {
	Device         string `json:"device"`
	MountPoint     string `json:"mount_point"`
	TotalBytes     uint64 `json:"total_bytes"`
	AvailableBytes uint64 `json:"available_bytes"`
	Type           string `json:"type,omitempty"`
	ReadOnly       bool   `json:"read_only,omitempty"`
}

// PowerInfo describes battery/power state. All fields are zero when the
// platform does not expose them.
type PowerInfo struct {
	State                string `json:"state"`      // charging, discharging, charged, unknown
	Percentage           int    `json:"percentage"` // 0-100
	TimeRemainingSeconds int64  `json:"time_remaining_seconds,omitempty"`
}

const (
	// ContainerDetected, ContainerNone, and ContainerUnsupported are the only
	// container states. Unsupported means the platform exposes no safe local
	// signal, not that the process is outside a container.
	ContainerDetected    = "detected"
	ContainerNone        = "none"
	ContainerUnsupported = "unsupported"
)

// PowerState values.
const (
	PowerStateCharging    = "charging"
	PowerStateDischarging = "discharging"
	PowerStateCharged     = "charged"
	PowerStateUnknown     = "unknown"
)

var errResourceUnsupported = errors.New("resource observation is unsupported on this platform")

// resourceProbe isolates native reads so bounds and cgroup arithmetic can be
// checked without a matching host.
type resourceProbe struct {
	goos, goarch    string
	now             func() time.Time
	cpuCount        func() int
	readFile        func(string) ([]byte, error)
	stat            func(string) (os.FileInfo, error)
	lookupEnv       func(string) (string, bool)
	physicalMemory  func() (uint64, error)
	uptime          func(time.Time) (time.Duration, error)
	storageCapacity func() ([]StorageInfo, error)
	powerCapacity   func() (*PowerInfo, error)
}

func newResourceProbe() resourceProbe {
	return resourceProbe{
		goos:            runtime.GOOS,
		goarch:          runtime.GOARCH,
		now:             func() time.Time { return time.Now().UTC() },
		cpuCount:        runtime.NumCPU,
		readFile:        os.ReadFile,
		stat:            os.Stat,
		lookupEnv:       os.LookupEnv,
		physicalMemory:  platformPhysicalMemory,
		uptime:          platformUptime,
		storageCapacity: platformStorageCapacity,
		powerCapacity:   platformPowerCapacity,
	}
}

// DiscoverResources observes the local machine's CPU, memory, container state,
// storage, power/battery, and uptime. It reads local files and system counters
// only: no command is launched, no network is contacted, and no identity or
// protocol is attached.
func DiscoverResources(ctx context.Context) (ResourceInfo, error) {
	if err := ctx.Err(); err != nil {
		return ResourceInfo{}, err
	}
	return discoverResources(ctx, newResourceProbe())
}

func discoverResources(ctx context.Context, probe resourceProbe) (ResourceInfo, error) {
	if err := ctx.Err(); err != nil {
		return ResourceInfo{}, err
	}
	now := probe.now().UTC()
	logical := probe.cpuCount()
	if logical < 1 {
		logical = 1
	}
	result := ResourceInfo{
		OS: probe.goos, Architecture: probe.goarch, ObservedAt: now,
		LogicalCPUs: logical,
		Container:   containerInfo(probe),
	}
	result.CPUCapacity, result.CPUStatus = cpuCapacity(probe, logical)
	result.MemoryBytes, result.MemoryCapacityBytes, result.MemoryStatus = memoryCapacity(probe, now)
	storage, err := probe.storageCapacity()
	if err != nil {
		return ResourceInfo{}, err
	}
	result.Storage = storage
	power, err := probe.powerCapacity()
	if err != nil {
		return ResourceInfo{}, err
	}
	result.Power = power
	result.Uptime, result.BootTime, result.UptimeStatus = uptimeObservation(probe, now)
	return result, nil
}

func cpuCapacity(probe resourceProbe, logical int) (float64, CollectionStatus) {
	if probe.goos != "linux" {
		return float64(logical), CollectionStatus{State: "complete", Detail: "logical CPU count"}
	}
	capacity, source, err := linuxCPUCapacity(probe.readFile, float64(logical))
	status := CollectionStatus{State: "complete", Detail: source}
	if err != nil {
		status = CollectionStatus{State: "partial", Detail: err.Error()}
	}
	return capacity, status
}

func memoryCapacity(probe resourceProbe, now time.Time) (uint64, uint64, CollectionStatus) {
	if probe.goos == "linux" {
		physical, effective, source, err := linuxMemoryCapacity(probe.readFile)
		if physical == 0 && effective == 0 {
			detail := "Linux memory capacity is unavailable"
			if err != nil {
				detail = err.Error()
			}
			return 0, 0, CollectionStatus{State: "unsupported", Detail: detail}
		}
		status := CollectionStatus{State: "complete", Detail: source}
		if err != nil {
			status = CollectionStatus{State: "partial", Detail: err.Error()}
		}
		return physical, effective, status
	}
	physical, err := probe.physicalMemory()
	if err != nil || physical == 0 {
		detail := "physical memory capacity is unavailable"
		if err != nil {
			detail = err.Error()
		}
		return 0, 0, CollectionStatus{State: "unsupported", Detail: detail}
	}
	return physical, physical, CollectionStatus{State: "complete", Detail: "physical memory capacity"}
}

func uptimeObservation(probe resourceProbe, now time.Time) (*time.Duration, *time.Time, CollectionStatus) {
	uptime, err := probe.uptime(now)
	if err != nil || uptime < 0 {
		detail := "uptime is unavailable"
		if err != nil {
			detail = err.Error()
		}
		return nil, nil, CollectionStatus{State: "unsupported", Detail: detail}
	}
	boot := now.Add(-uptime)
	return &uptime, &boot, CollectionStatus{State: "complete"}
}

func containerInfo(probe resourceProbe) ContainerInfo {
	if probe.goos != "linux" {
		return ContainerInfo{State: ContainerUnsupported, Evidence: []string{}}
	}
	evidence := []string{}
	if value, ok := probe.lookupEnv("container"); ok && strings.TrimSpace(value) != "" {
		evidence = append(evidence, "container-environment")
	}
	if value, ok := probe.lookupEnv("KUBERNETES_SERVICE_HOST"); ok && strings.TrimSpace(value) != "" {
		evidence = append(evidence, "kubernetes-environment")
	}
	for path, marker := range map[string]string{
		"/.dockerenv":        "docker-marker",
		"/run/.containerenv": "podman-marker",
	} {
		if _, err := probe.stat(path); err == nil {
			evidence = append(evidence, marker)
		}
	}
	for _, path := range []string{"/proc/1/cgroup", "/proc/self/cgroup"} {
		payload, err := probe.readFile(path)
		if err != nil {
			continue
		}
		content := strings.ToLower(string(payload))
		for _, signature := range []struct{ name, marker string }{
			{"/docker/", "docker-cgroup"},
			{"/docker-", "docker-cgroup"},
			{"/containerd/", "containerd-cgroup"},
			{"/podman/", "podman-cgroup"},
			{"/kubepods/", "kubernetes-cgroup"},
			{"/lxc/", "lxc-cgroup"},
		} {
			if strings.Contains(content, signature.name) {
				evidence = append(evidence, signature.marker)
			}
		}
	}
	unique := map[string]bool{}
	cleaned := []string{}
	for _, value := range evidence {
		if unique[value] {
			continue
		}
		unique[value] = true
		cleaned = append(cleaned, value)
	}
	sort.Strings(cleaned)
	if len(cleaned) == 0 {
		return ContainerInfo{State: ContainerNone, Evidence: cleaned}
	}
	return ContainerInfo{Detected: true, State: ContainerDetected, Evidence: cleaned}
}

// linuxCPUCapacity applies cgroup v2 and v1 CPU quotas to a host CPU count. A
// missing quota means the whole host is available. Unreadable or malformed
// quota files are reported as an error so the caller can mark the observation
// partial instead of claiming a capacity.
func linuxCPUCapacity(readFile func(string) ([]byte, error), host float64) (float64, string, error) {
	if readFile == nil {
		return host, "logical CPU count", nil
	}
	if payload, err := readFile("/sys/fs/cgroup/cpu.max"); err == nil {
		fields := strings.Fields(string(payload))
		if len(fields) != 2 {
			return host, "logical CPU count", fmt.Errorf("cgroup v2 cpu.max is malformed")
		}
		if fields[0] == "max" {
			return host, "logical CPU count", nil
		}
		quota, quotaErr := strconv.ParseUint(fields[0], 10, 64)
		period, periodErr := strconv.ParseUint(fields[1], 10, 64)
		if quotaErr != nil || periodErr != nil || quota == 0 || period == 0 {
			return host, "logical CPU count", fmt.Errorf("cgroup v2 cpu.max is malformed")
		}
		capacity := boundedCPUCapacity(host, quota, period)
		if capacity >= host {
			return host, "logical CPU count", nil
		}
		return capacity, "cgroup-v2", nil
	}
	quotaPayload, quotaErr := readFile("/sys/fs/cgroup/cpu/cpu.cfs_quota_us")
	if quotaErr != nil {
		if errors.Is(quotaErr, os.ErrNotExist) {
			return host, "logical CPU count", nil
		}
		return host, "logical CPU count", fmt.Errorf("cgroup v1 quota is unreadable")
	}
	periodPayload, periodErr := readFile("/sys/fs/cgroup/cpu/cpu.cfs_period_us")
	if periodErr != nil {
		return host, "logical CPU count", fmt.Errorf("cgroup v1 period is unreadable")
	}
	quota, err := strconv.ParseInt(strings.TrimSpace(string(quotaPayload)), 10, 64)
	if err != nil {
		return host, "logical CPU count", fmt.Errorf("cgroup v1 quota is malformed")
	}
	if quota < 0 {
		return host, "logical CPU count", nil
	}
	period, err := strconv.ParseUint(strings.TrimSpace(string(periodPayload)), 10, 64)
	if err != nil || period == 0 {
		return host, "logical CPU count", fmt.Errorf("cgroup v1 period is malformed")
	}
	if quota == 0 {
		return 0, "cgroup-v1", nil
	}
	capacity := boundedCPUCapacity(host, uint64(quota), period)
	if capacity >= host {
		return host, "logical CPU count", nil
	}
	return capacity, "cgroup-v1", nil
}

func boundedCPUCapacity(host float64, quota, period uint64) float64 {
	capacity := float64(quota) / float64(period)
	if capacity <= 0 || capacity > host {
		return host
	}
	return capacity
}

// linuxMemoryCapacity returns the physical and container-effective memory
// capacity. A cgroup limit at or above the host value, or the kernel's
// "unlimited" sentinel, leaves the host value in place.
func linuxMemoryCapacity(readFile func(string) ([]byte, error)) (uint64, uint64, string, error) {
	if readFile == nil {
		return 0, 0, "", errResourceUnsupported
	}
	physical := uint64(0)
	if payload, err := readFile("/proc/meminfo"); err == nil {
		for _, line := range strings.Split(string(payload), "\n") {
			fields := strings.Fields(line)
			if len(fields) >= 2 && fields[0] == "MemTotal:" {
				physical = parseUnsigned(fields[1], 1024)
				break
			}
		}
		if physical == 0 {
			return 0, 0, "physical memory unavailable", fmt.Errorf("proc meminfo has no MemTotal")
		}
	} else {
		return 0, 0, "physical memory unavailable", fmt.Errorf("proc meminfo is unreadable")
	}
	limit := uint64(0)
	source := "physical memory capacity"
	for _, path := range []string{"/sys/fs/cgroup/memory.max", "/sys/fs/cgroup/memory/memory.limit_in_bytes"} {
		payload, err := readFile(path)
		if err != nil {
			continue
		}
		value := strings.TrimSpace(string(payload))
		if value == "max" {
			return physical, physical, "cgroup unlimited", nil
		}
		parsed := parseUnsigned(value, 1)
		if parsed == 0 {
			return physical, physical, "physical memory capacity", fmt.Errorf("%s is malformed", path)
		}
		// The v1 kernel reports an "unlimited" value near the uint64 ceiling.
		if parsed < 1<<60 {
			limit = parsed
			source = "cgroup memory limit"
		}
		break
	}
	if limit == 0 || limit >= physical {
		return physical, physical, "physical memory capacity", nil
	}
	return physical, limit, source, nil
}

func parseUnsigned(value string, multiplier uint64) uint64 {
	parsed, err := strconv.ParseUint(strings.TrimSpace(value), 10, 64)
	if err != nil || multiplier == 0 || parsed > ^uint64(0)/multiplier {
		return 0
	}
	return parsed * multiplier
}

// parseProcUptime reads the idle-inclusive uptime from /proc/uptime.
func parseProcUptime(payload []byte) (time.Duration, error) {
	fields := strings.Fields(string(payload))
	if len(fields) == 0 {
		return 0, fmt.Errorf("proc uptime is empty")
	}
	seconds, err := strconv.ParseFloat(fields[0], 64)
	if err != nil || seconds < 0 {
		return 0, fmt.Errorf("proc uptime is malformed")
	}
	return time.Duration(seconds * float64(time.Second)), nil
}

// parseProcBootTime reads the absolute boot second from /proc/stat.
func parseProcBootTime(payload []byte) (time.Time, error) {
	for _, line := range strings.Split(string(payload), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || fields[0] != "btime" {
			continue
		}
		seconds, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil || seconds <= 0 {
			return time.Time{}, fmt.Errorf("proc boot time is malformed")
		}
		return time.Unix(seconds, 0).UTC(), nil
	}
	return time.Time{}, fmt.Errorf("proc stat has no boot time")
}
