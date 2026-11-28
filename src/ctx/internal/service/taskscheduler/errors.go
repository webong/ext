package taskscheduler

import "errors"

// ErrUnsupported reports a host that has no Windows Task Scheduler.
var ErrUnsupported = errors.New("Task Scheduler is only available on Windows")
