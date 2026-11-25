package supervisor

import "time"

type Artifact struct {
	ID       string `json:"id"`
	Revision string `json:"revision"`
	Checksum string `json:"checksum"`
}
type Endpoint struct {
	ID        string `json:"id"`
	Address   string `json:"address"`
	Transport string `json:"transport"`
	Direction string `json:"direction,omitempty"`
}
type Connection struct {
	ID             string            `json:"id"`
	SourceEndpoint string            `json:"source_endpoint"`
	TargetEndpoint string            `json:"target_endpoint"`
	Metadata       map[string]string `json:"metadata,omitempty"`
}
type State string

const (
	StateStarting   State = "starting"
	StateRunning    State = "running"
	StateReady      State = "ready"
	StateStopping   State = "stopping"
	StateStopped    State = "stopped"
	StateCrashed    State = "crashed"
	StateRestarting State = "restarting"
	StateOrphaned   State = "orphaned"
)

type Instance struct {
	ID              string       `json:"id"`
	Artifact        Artifact     `json:"artifact"`
	State           State        `json:"state"`
	PID             int          `json:"pid,omitempty"`
	ProcessIdentity string       `json:"process_identity,omitempty"`
	StartedAt       time.Time    `json:"started_at,omitempty"`
	UpdatedAt       time.Time    `json:"updated_at"`
	ExitCode        *int         `json:"exit_code,omitempty"`
	CrashCount      int          `json:"crash_count"`
	Health          string       `json:"health,omitempty"`
	LastError       string       `json:"last_error,omitempty"`
	Endpoints       []Endpoint   `json:"endpoints,omitempty"`
	Connections     []Connection `json:"connections,omitempty"`
	Dependencies    []string     `json:"dependencies,omitempty"`
}
type Orphan struct {
	ID              string
	PID             int
	ProcessIdentity string
	Alive           bool
}
