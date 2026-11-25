package plugin

import "context"

// Endpoint is the shared host/guest interface. Transport clients implement it
// for hosts; Guest implements it for plugin authors. The interface carries no
// transport or process ownership. Backend adds host-side connection cleanup.
type Endpoint interface {
	Handshake(context.Context) (Descriptor, error)
	Invoke(context.Context, Request) (Response, error)
}

// Backend is a consumer-selected transport binding. Implementations must honor
// cancellation, permit concurrent Invoke calls (possibly serialized internally),
// and make Close interrupt transport I/O. In-host native execution has no forced
// termination: native Go handlers cooperate with contexts; C ABI cancellation
// releases the caller while native work and cleanup may remain outstanding.
// Backends never decide product authorization. A CLI binding may preserve raw
// streams outside this JSON invocation API and use ValidateRequest directly.
type Backend interface {
	Endpoint
	Close() error
}
