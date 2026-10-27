package system

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"sort"
	"testing"
	"time"
)

type fakeResourceProbe struct {
	files   map[string]string
	env     map[string]string
	present map[string]bool
	memory  uint64
	uptime  time.Duration
	now     time.Time
	goos    string
	cpus    int
}

func (f fakeResourceProbe) probe() resourceProbe {
	return resourceProbe{
		goos: f.goos, goarch: "arm64",
		now:      func() time.Time { return f.now },
		cpuCount: func() int { return f.cpus },
		readFile: func(path string) ([]byte, error) {
			if value, ok := f.files[path]; ok {
				return []byte(value), nil
			}
			return nil, fs.ErrNotExist
		},
		stat: func(path string) (os.FileInfo, error) {
			if f.present[path] {
				return fakeFileInfo{}, nil
			}
			return nil, fs.ErrNotExist
		},
		lookupEnv: func(key string) (string, bool) {
			value, ok := f.env[key]
			return value, ok
		},
		physicalMemory: func() (uint64, error) {
			if f.memory == 0 {
				return 0, errResourceUnsupported
			}
			return f.memory, nil
		},
		uptime: func(time.Time) (time.Duration, error) {
			if f.uptime < 0 {
				return 0, errResourceUnsupported
			}
			return f.uptime, nil
		},
	}
}

type fakeFileInfo struct{}

func (fakeFileInfo) Name() string       { return "marker" }
func (fakeFileInfo) Size() int64        { return 0 }
func (fakeFileInfo) Mode() os.FileMode  { return 0 }
func (fakeFileInfo) ModTime() time.Time { return time.Time{} }
func (fakeFileInfo) IsDir() bool        { return false }
func (fakeFileInfo) Sys() any           { return nil }

func TestDiscoverResourcesDarwinUsesNativeCapacity(t *testing.T) {
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	result, err := discoverResources(context.Background(), fakeResourceProbe{
		goos: "darwin", cpus: 10, memory: 32 << 30, uptime: 2 * time.Hour, now: now,
	}.probe())
	if err != nil {
		t.Fatal(err)
	}
	if result.OS != "darwin" || result.Architecture != "arm64" || result.LogicalCPUs != 10 {
		t.Fatalf("identity = %#v", result)
	}
	if result.CPUCapacity != 10 || result.CPUStatus.State != "complete" {
		t.Fatalf("cpu = %v %#v", result.CPUCapacity, result.CPUStatus)
	}
	if result.MemoryBytes != 32<<30 || result.MemoryCapacityBytes != 32<<30 || result.MemoryStatus.State != "complete" {
		t.Fatalf("memory = %d %d %#v", result.MemoryBytes, result.MemoryCapacityBytes, result.MemoryStatus)
	}
	if result.Container.State != ContainerUnsupported || result.Container.Detected {
		t.Fatalf("container = %#v", result.Container)
	}
	if result.Uptime == nil || *result.Uptime != 2*time.Hour || result.BootTime == nil || !result.BootTime.Equal(now.Add(-2*time.Hour)) {
		t.Fatalf("uptime = %v boot = %v", result.Uptime, result.BootTime)
	}
}

func TestDiscoverResourcesLinuxAppliesCgroupV2(t *testing.T) {
	result, err := discoverResources(context.Background(), fakeResourceProbe{
		goos: "linux", cpus: 8, uptime: time.Hour, now: time.Now().UTC(),
		files: map[string]string{
			"/proc/meminfo":             "MemTotal:       8388608 kB\nMemFree: 1 kB\n",
			"/sys/fs/cgroup/cpu.max":    "200000 100000\n",
			"/sys/fs/cgroup/memory.max": "4294967296\n",
			"/proc/1/cgroup":            "0::/docker/abcdef\n",
		},
		env:     map[string]string{"container": "docker"},
		present: map[string]bool{"/.dockerenv": true},
	}.probe())
	if err != nil {
		t.Fatal(err)
	}
	if result.CPUCapacity != 2 {
		t.Fatalf("cpu capacity = %v", result.CPUCapacity)
	}
	if result.MemoryBytes != 8<<30 || result.MemoryCapacityBytes != 4<<30 {
		t.Fatalf("memory = %d -> %d", result.MemoryBytes, result.MemoryCapacityBytes)
	}
	if !result.Container.Detected || result.Container.State != ContainerDetected {
		t.Fatalf("container = %#v", result.Container)
	}
	if !sort.StringsAreSorted(result.Container.Evidence) {
		t.Fatalf("evidence is not sorted: %#v", result.Container.Evidence)
	}
}

func TestDiscoverResourcesLinuxAppliesCgroupV1AndIgnoresUnlimited(t *testing.T) {
	result, err := discoverResources(context.Background(), fakeResourceProbe{
		goos: "linux", cpus: 4, uptime: time.Hour, now: time.Now().UTC(),
		files: map[string]string{
			"/proc/meminfo":                               "MemTotal:       4194304 kB\n",
			"/sys/fs/cgroup/cpu/cpu.cfs_quota_us":         "150000\n",
			"/sys/fs/cgroup/cpu/cpu.cfs_period_us":        "100000\n",
			"/sys/fs/cgroup/memory/memory.limit_in_bytes": "9223372036854771712\n",
		},
	}.probe())
	if err != nil {
		t.Fatal(err)
	}
	if result.CPUCapacity != 1.5 {
		t.Fatalf("cpu capacity = %v", result.CPUCapacity)
	}
	if result.MemoryBytes != 4<<30 || result.MemoryCapacityBytes != 4<<30 {
		t.Fatalf("memory = %d -> %d", result.MemoryBytes, result.MemoryCapacityBytes)
	}
	if result.CPUStatus.Detail != "cgroup-v1" {
		t.Fatalf("cpu detail = %q", result.CPUStatus.Detail)
	}
}

func TestDiscoverResourcesLinuxReportsMalformedCgroupsAsPartial(t *testing.T) {
	result, err := discoverResources(context.Background(), fakeResourceProbe{
		goos: "linux", cpus: 8, uptime: time.Hour, now: time.Now().UTC(),
		files: map[string]string{
			"/proc/meminfo":             "MemTotal:       8388608 kB\n",
			"/sys/fs/cgroup/cpu.max":    "not-a-number\n",
			"/sys/fs/cgroup/memory.max": "oops\n",
		},
	}.probe())
	if err != nil {
		t.Fatal(err)
	}
	if result.CPUStatus.State != "partial" || result.CPUCapacity != 8 {
		t.Fatalf("cpu = %v %#v", result.CPUCapacity, result.CPUStatus)
	}
	if result.MemoryStatus.State != "partial" || result.MemoryCapacityBytes != 8<<30 {
		t.Fatalf("memory = %#v %d", result.MemoryStatus, result.MemoryCapacityBytes)
	}
}

func TestDiscoverResourcesLinuxWithoutCgroupUsesHost(t *testing.T) {
	result, err := discoverResources(context.Background(), fakeResourceProbe{
		goos: "linux", cpus: 6, uptime: time.Hour, now: time.Now().UTC(),
		files: map[string]string{"/proc/meminfo": "MemTotal:       2097152 kB\n"},
	}.probe())
	if err != nil {
		t.Fatal(err)
	}
	if result.CPUCapacity != 6 || result.CPUStatus.State != "complete" {
		t.Fatalf("cpu = %v %#v", result.CPUCapacity, result.CPUStatus)
	}
	if result.MemoryBytes != 2<<30 || result.MemoryCapacityBytes != 2<<30 {
		t.Fatalf("memory = %d -> %d", result.MemoryBytes, result.MemoryCapacityBytes)
	}
	if result.Container.State != ContainerNone || result.Container.Detected {
		t.Fatalf("container = %#v", result.Container)
	}
}

func TestDiscoverResourcesUnsupportedPlatformIsExplicit(t *testing.T) {
	result, err := discoverResources(context.Background(), fakeResourceProbe{
		goos: "plan9", cpus: 2, uptime: -1, now: time.Now().UTC(),
	}.probe())
	if err != nil {
		t.Fatal(err)
	}
	if result.MemoryStatus.State != "unsupported" || result.MemoryBytes != 0 || result.MemoryCapacityBytes != 0 {
		t.Fatalf("memory = %#v", result.MemoryStatus)
	}
	if result.UptimeStatus.State != "unsupported" || result.Uptime != nil || result.BootTime != nil {
		t.Fatalf("uptime = %#v", result.UptimeStatus)
	}
	if result.Container.State != ContainerUnsupported {
		t.Fatalf("container = %#v", result.Container)
	}
	if result.CPUCapacity != 2 {
		t.Fatalf("cpu capacity = %v", result.CPUCapacity)
	}
}

func TestResourceTextParsersRejectGarbage(t *testing.T) {
	if value, err := parseProcUptime([]byte("1234.50 999.00\n")); err != nil || value != 1234*time.Second+500*time.Millisecond {
		t.Fatalf("uptime = %v %v", value, err)
	}
	if _, err := parseProcUptime([]byte("garbage\n")); err == nil {
		t.Fatal("malformed uptime accepted")
	}
	if _, err := parseProcUptime(nil); err == nil {
		t.Fatal("empty uptime accepted")
	}
	if boot, err := parseProcBootTime([]byte("cpu 1 2 3\nbtime 1700000000\n")); err != nil || !boot.Equal(time.Unix(1700000000, 0).UTC()) {
		t.Fatalf("boot = %v %v", boot, err)
	}
	if _, err := parseProcBootTime([]byte("btime not-a-time\n")); err == nil {
		t.Fatal("malformed boot time accepted")
	}
	if _, err := parseProcBootTime([]byte("cpu 1 2 3\n")); err == nil {
		t.Fatal("missing boot time accepted")
	}
}

func TestObserveHostProjectsResourcesWithoutNetwork(t *testing.T) {
	g, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	now := time.Now().UTC()
	uptime := time.Hour
	boot := now.Add(-uptime)
	resources := &ResourceInfo{
		OS: "linux", Architecture: "amd64", ObservedAt: now, LogicalCPUs: 4,
		CPUCapacity: 2, CPUStatus: CollectionStatus{State: "complete"},
		MemoryBytes: 8 << 30, MemoryCapacityBytes: 4 << 30, MemoryStatus: CollectionStatus{State: "complete"},
		Container: ContainerInfo{Detected: true, State: ContainerDetected, Evidence: []string{"docker-marker"}},
		Uptime:    &uptime, BootTime: &boot, UptimeStatus: CollectionStatus{State: "complete"},
	}
	if err = g.ObserveHost(context.Background(), HostInventory{Resources: resources}); err != nil {
		t.Fatal(err)
	}
	vertex, err := g.Store.GetVertex(context.Background(), Namespace, "host-resources/local")
	if err != nil {
		t.Fatal(err)
	}
	if vertex.Attributes["os"] != "linux" || vertex.Attributes["container"] == nil {
		t.Fatalf("attributes = %#v", vertex.Attributes)
	}
	for _, key := range []string{"interfaces", "network", "addresses"} {
		if _, exists := vertex.Attributes[key]; exists {
			t.Fatalf("resource projection duplicated network state under %q", key)
		}
	}
	if _, err = g.Store.GetEdge(context.Background(), Namespace, "machine-host-resources/local"); err != nil {
		t.Fatalf("resource edge missing: %v", err)
	}
	if err = g.ObserveHost(context.Background(), HostInventory{Resources: &ResourceInfo{OS: "linux"}}); err == nil {
		t.Fatal("incomplete resource observation accepted")
	}
}

func TestDiscoverResourcesHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := DiscoverResources(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled observation = %v", err)
	}
}

func TestLinuxCPUCapacityNeverExceedsHost(t *testing.T) {
	read := func(path string) ([]byte, error) {
		if path == "/sys/fs/cgroup/cpu.max" {
			return []byte("1000000 100000"), nil
		}
		return nil, fs.ErrNotExist
	}
	capacity, source, err := linuxCPUCapacity(read, 4)
	if err != nil || capacity != 4 || source != "logical CPU count" {
		t.Fatalf("oversized quota = %v %q %v", capacity, source, err)
	}
	read = func(path string) ([]byte, error) {
		switch path {
		case "/sys/fs/cgroup/cpu/cpu.cfs_quota_us":
			return []byte("-1\n"), nil
		case "/sys/fs/cgroup/cpu/cpu.cfs_period_us":
			return []byte("100000\n"), nil
		}
		return nil, fs.ErrNotExist
	}
	if capacity, _, err = linuxCPUCapacity(read, 4); err != nil || capacity != 4 {
		t.Fatalf("unlimited v1 quota = %v %v", capacity, err)
	}
	read = func(path string) ([]byte, error) { return nil, os.ErrPermission }
	if _, _, err := linuxCPUCapacity(read, 4); err == nil {
		t.Fatal("unreadable cgroup accepted")
	}
}
