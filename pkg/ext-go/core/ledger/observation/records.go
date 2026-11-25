// Package observation owns the ledger's wire records. These records contain no OS mechanisms.
package observation

import "time"

type ContainerInfo struct {
	Detected bool     `json:"detected"`
	State    string   `json:"state"`
	Evidence []string `json:"evidence"`
}
type FilesystemInfo struct {
	MountPoint  string           `json:"mount_point"`
	Source      string           `json:"source"`
	Type        string           `json:"type"`
	ReadOnly    bool             `json:"read_only"`
	Space       *FilesystemSpace `json:"space,omitempty"`
	Label       string           `json:"label,omitempty"`
	DriveKind   string           `json:"drive_kind,omitempty"`
	DetailError string           `json:"detail_error,omitempty"`
}
type FilesystemSpace struct {
	TotalBytes     uint64 `json:"total_bytes"`
	FreeBytes      uint64 `json:"free_bytes"`
	AvailableBytes uint64 `json:"available_bytes"`
}
type PowerInfo struct {
	State                string `json:"state"`
	Percentage           int    `json:"percentage"`
	TimeRemainingSeconds int64  `json:"time_remaining_seconds,omitempty"`
}
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
	UptimeSeconds       *float64         `json:"uptime_seconds,omitempty"`
	BootTime            *time.Time       `json:"boot_time,omitempty"`
	UptimeStatus        CollectionStatus `json:"uptime_status"`
}
type ShellInfo struct {
	Name              string   `json:"name"`
	Path              string   `json:"path"`
	ResolvedPath      string   `json:"resolved_path"`
	ConfiguredDefault bool     `json:"configured_default"`
	Sources           []string `json:"sources"`
}
type StorageInfo struct {
	Device         string `json:"device"`
	MountPoint     string `json:"mount_point"`
	TotalBytes     uint64 `json:"total_bytes"`
	AvailableBytes uint64 `json:"available_bytes"`
	Type           string `json:"type,omitempty"`
	ReadOnly       bool   `json:"read_only,omitempty"`
}
type WebviewInfo struct {
	Name         string `json:"name"`
	Engine       string `json:"engine"`
	API          string `json:"api"`
	Version      string `json:"version,omitempty"`
	ABIVersion   string `json:"abi_version,omitempty"`
	Architecture string `json:"architecture,omitempty"`
	Location     string `json:"location"`
	ResolvedPath string `json:"resolved_path,omitempty"`
	Scope        string `json:"scope"`
	Source       string `json:"source"`
	DetailError  string `json:"detail_error,omitempty"`
}
type ApplicationInfo struct {
	Name string `json:"name"`
	Path string `json:"path"`
}
type CollectionStatus struct {
	State      string     `json:"state"`
	Reason     string     `json:"reason,omitempty"`
	Detail     string     `json:"detail,omitempty"`
	ObservedAt *time.Time `json:"observed_at,omitempty"`
}
type ProcessInfo struct {
	PID         int                         `json:"pid"`
	ParentPID   int                         `json:"parent_pid"`
	Name        string                      `json:"name"`
	StartID     string                      `json:"start_id,omitempty"`
	Executable  string                      `json:"executable,omitempty"`
	Owner       string                      `json:"owner,omitempty"`
	Application *ApplicationInfo            `json:"application,omitempty"`
	Usage       *ProcessUsage               `json:"usage,omitempty"`
	Coverage    map[string]CollectionStatus `json:"coverage"`
	Resources   []ProcessResource           `json:"resources,omitempty"`
}
type ProcessOptions struct {
	MaxProcesses int
	MaxResources int
	Timeout      time.Duration
}
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
type ProcessSnapshot struct {
	HostID      string           `json:"host_id"`
	Scope       string           `json:"scope"`
	ObservedAt  time.Time        `json:"observed_at"`
	Enumeration CollectionStatus `json:"enumeration"`
	Processes   []ProcessInfo    `json:"processes"`
}
type ProcessUsage struct {
	ResidentBytes *uint64  `json:"resident_bytes,omitempty"`
	VirtualBytes  *uint64  `json:"virtual_bytes,omitempty"`
	CPUSeconds    *float64 `json:"cpu_seconds,omitempty"`
	Threads       *uint32  `json:"threads,omitempty"`
}
type ProgramCandidate struct {
	Name       string         `json:"name"`
	Path       string         `json:"path"`
	RealPath   string         `json:"realpath"`
	Version    string         `json:"version,omitempty"`
	Source     string         `json:"source"`
	OnPath     bool           `json:"on_path,omitempty"`
	Installers []string       `json:"installers,omitempty"`
	Attributes map[string]any `json:"attributes,omitempty"`
}
type AccessibilityElement struct {
	ID             string         `json:"id"`
	ParentID       string         `json:"parent_id,omitempty"`
	PID            int            `json:"pid"`
	ProcessStartID string         `json:"process_start_id"`
	Role           string         `json:"role"`
	Identifier     string         `json:"identifier,omitempty"`
	Label          string         `json:"label,omitempty"`
	Value          string         `json:"value,omitempty"`
	Bounds         *SurfaceBounds `json:"bounds,omitempty"`
	Actions        []string       `json:"actions,omitempty"`
	Enabled        *bool          `json:"enabled,omitempty"`
	Focused        *bool          `json:"focused,omitempty"`
}
type AccessibilitySnapshot struct {
	Platform   string                 `json:"platform"`
	ObservedAt time.Time              `json:"observed_at"`
	Status     CollectionStatus       `json:"status"`
	Elements   []AccessibilityElement `json:"elements"`
}
type DesktopEndpoint struct {
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
}
type DesktopInfo struct {
	Platform      string          `json:"platform"`
	Interactive   bool            `json:"interactive"`
	Containerized bool            `json:"containerized"`
	Capabilities  []string        `json:"capabilities,omitempty"`
	X11           DesktopEndpoint `json:"x11"`
	Wayland       DesktopEndpoint `json:"wayland"`
	Recording     RecordingInfo   `json:"recording"`
	Reason        string          `json:"reason,omitempty"`
	ObservedAt    time.Time       `json:"observed_at"`
}
type DisplayInfo struct {
	ID      string        `json:"id"`
	Bounds  SurfaceBounds `json:"bounds"`
	Scale   float64       `json:"scale,omitempty"`
	Primary bool          `json:"primary"`
}
type PermissionInfo struct {
	State  string `json:"state"`
	Reason string `json:"reason,omitempty"`
	Scope  string `json:"scope,omitempty"`
	Detail string `json:"detail,omitempty"`
}
type PermissionSnapshot struct {
	Platform        string         `json:"platform"`
	ObservedAt      time.Time      `json:"observed_at"`
	Accessibility   PermissionInfo `json:"accessibility"`
	ScreenRecording PermissionInfo `json:"screen_recording"`
}
type RecordingInfo struct {
	Status           string    `json:"status"`
	EncoderAvailable bool      `json:"encoder_available"`
	CaptureAvailable bool      `json:"capture_available"`
	Permission       string    `json:"permission"`
	Formats          []string  `json:"formats,omitempty"`
	Message          string    `json:"message,omitempty"`
	CheckedAt        time.Time `json:"checked_at"`
}
type SurfaceBounds struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}
type SurfaceSnapshot struct {
	Platform        string           `json:"platform"`
	CoordinateSpace string           `json:"coordinate_space"`
	ObservedAt      time.Time        `json:"observed_at"`
	Displays        []DisplayInfo    `json:"displays"`
	Windows         []WindowInfo     `json:"windows"`
	DisplayStatus   CollectionStatus `json:"display_status"`
	WindowStatus    CollectionStatus `json:"window_status"`
}
type WindowInfo struct {
	ID             string        `json:"id"`
	PID            int           `json:"pid"`
	ProcessStartID string        `json:"process_start_id"`
	Bounds         SurfaceBounds `json:"bounds"`
	OnScreen       bool          `json:"on_screen"`
}
