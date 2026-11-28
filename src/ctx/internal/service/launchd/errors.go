package launchd

import "errors"

// ErrUnsupported reports a host that has no launchd user domain.
var ErrUnsupported = errors.New("launchd is only available on macOS")
