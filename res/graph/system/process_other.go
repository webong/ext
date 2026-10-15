//go:build !darwin && !linux && !windows

package systemgraph

import (
	"context"
	"errors"
)

func processHostID(context.Context) (string, error) {
	return "", errors.New("process discovery is unsupported on this OS")
}
func platformProcesses(context.Context, int) ([]ProcessInfo, CollectionStatus, error) {
	return nil, unsupportedProcessStatus("process collector unavailable"), nil
}
func platformProcess(context.Context, int) (ProcessInfo, error) {
	return ProcessInfo{}, errors.New("process inspection is unsupported on this OS")
}
func platformProcessResources(_ context.Context, p *ProcessInfo, _ int) {
	for _, kind := range []string{"file", "mapping", "socket", "ipc", "usage"} {
		p.Coverage[kind] = unsupportedProcessStatus("process collector unavailable")
	}
}
