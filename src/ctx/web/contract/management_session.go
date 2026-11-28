package contract

import "context"

// SessionTarget is a caller-selected browser page endpoint. Protocol is
// intentionally supplied by the caller or discovered target; no browser or
// profile is chosen implicitly.
type SessionTarget struct {
	ID       string `json:"id,omitempty"`
	Protocol string `json:"protocol"` // cdp or bidi
	Endpoint string `json:"endpoint"`
	URL      string `json:"url,omitempty"`
	Title    string `json:"title,omitempty"`
}

// InjectionOptions describe when and in which JavaScript world code runs.
// Browser permissions and page policy continue to apply.
type InjectionOptions struct {
	RunAt string `json:"runAt,omitempty"`
	World string `json:"world,omitempty"`
}

// UserscriptRegistration contains the reviewed source and URL scope a session
// runtime needs to register a script and replay it after navigation.
type UserscriptRegistration struct {
	ID             string   `json:"id"`
	Revision       string   `json:"revision"`
	Source         string   `json:"source"`
	Matches        []string `json:"matches"`
	ExcludeMatches []string `json:"excludeMatches,omitempty"`
}

type ReplayResult struct {
	Registered int `json:"registered"`
	Restored   int `json:"restored"`
	Removed    int `json:"removed"`
}

// PageSessionRuntime is the reusable boundary for browser target discovery and
// page/session attachment. An implementation may use CDP, WebDriver BiDi, or
// another browser-native protocol.
type PageSessionRuntime interface {
	DiscoverTargets(context.Context, string) ([]SessionTarget, error)
	Connect(context.Context, string, SessionTarget) (PageSession, error)
}

// PageSession keeps page interaction and script execution out of the
// management workflow. Enabled userscripts remain registered for replay after
// subsequent navigation until the session is closed.
type PageSession interface {
	Navigate(context.Context, string) error
	Inject(context.Context, string, InjectionOptions) error
	ReplayUserscripts(context.Context, []UserscriptRegistration) (ReplayResult, error)
	Close(context.Context) error
}

// PageSessionState is optionally implemented by a native session so a workflow
// can stop promptly when the attachment ends. Implementations with automatic
// recovery keep Done open during a recoverable transport outage.
type PageSessionState interface {
	Done() <-chan struct{}
	Err() error
}
