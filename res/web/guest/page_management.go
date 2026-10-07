package guest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/webong/ext/res/web/contract"
	"github.com/webong/ext/res/web/extension"
	"github.com/webong/ext/res/web/userscript"
)

// PageSessionBackend supplies native page transport without a core provider
// switch. Adapter-specific endpoint options remain opaque until this call.
type PageSessionBackend interface {
	PageRuntime(context.Context, string, json.RawMessage) (contract.PageSessionRuntime, error)
}

type sessionInput struct {
	Target        contract.SessionTarget            `json:"target"`
	URL           string                            `json:"url"`
	Source        string                            `json:"source"`
	Options       contract.InjectionOptions         `json:"options"`
	Registrations []contract.UserscriptRegistration `json:"registrations"`
}

func (backend localManagementBackend) pageRuntime(ctx context.Context, profile string, raw json.RawMessage) (contract.PageSessionRuntime, error) {
	native, ok := backend.native.(PageSessionBackend)
	if !ok {
		return nil, fmt.Errorf("adapter %s has no native page session route", backend.browserName)
	}
	return native.PageRuntime(ctx, profile, raw)
}

func sessionDisconnected(page contract.PageSession) (<-chan struct{}, func() error) {
	if state, ok := page.(contract.PageSessionState); ok {
		return state.Done(), state.Err
	}
	return nil, func() error { return errors.New("page session disconnected") }
}

func (backend localManagementBackend) manageSession(ctx context.Context, profile, action string, raw json.RawMessage) (result any, status string, err error) {
	var input sessionInput
	if err := decodeManagementInput(raw, &input); err != nil {
		return nil, "", err
	}
	runtime, err := backend.pageRuntime(ctx, profile, raw)
	if err != nil {
		return nil, "", err
	}
	if action == "targets" {
		targets, err := runtime.DiscoverTargets(ctx, profile)
		return targets, "ready", err
	}
	page, err := runtime.Connect(ctx, profile, input.Target)
	if err != nil {
		return nil, "", err
	}
	defer func() { err = errors.Join(err, page.Close(context.Background())) }()
	keepAlive := false
	switch action {
	case "connect":
		result = input.Target
		status = "connected"
		keepAlive = true
	case "navigate":
		err = page.Navigate(ctx, input.URL)
		result = input.Target
		status = "navigated"
	case "inject":
		err = page.Inject(ctx, input.Source, input.Options)
		result = input.Target
		status = "injected"
		keepAlive = input.Options.RunAt == "document-start"
	case "replay":
		result, err = page.ReplayUserscripts(ctx, input.Registrations)
		status = "activated"
		keepAlive = true
	default:
		return nil, "", fmt.Errorf("unsupported page session action %q", action)
	}
	if err != nil || !keepAlive {
		return result, status, err
	}
	if err := writeManagementProgress(backend.progress, extension.InstallResult{Status: status, Browser: backend.browserName, Profile: profile, NextAction: "The page session is attached until this command is interrupted; its registrations are removed when the command ends."}); err != nil {
		return nil, "", err
	}
	done, failure := sessionDisconnected(page)
	select {
	case <-ctx.Done():
		return result, status, nil
	case <-done:
		return nil, "", failure()
	}
}

// activateStoredUserscripts is portable store reconciliation. The adapter's
// page session owns registration, execution, replay, and transport cleanup.
func (backend localManagementBackend) activateStoredUserscripts(ctx context.Context, profile, directory, target, id string, selected contract.SessionTarget, raw json.RawMessage) (result any, err error) {
	runtime, err := backend.pageRuntime(ctx, profile, raw)
	if err != nil {
		return nil, err
	}
	if id != "" {
		record, err := userscript.Load(directory, target, id)
		if err != nil {
			return nil, err
		}
		if !record.Enabled {
			return nil, fmt.Errorf("userscript %q is disabled", id)
		}
	}
	page, err := runtime.Connect(ctx, profile, selected)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, page.Close(context.Background())) }()
	reconcile := func() (contract.ReplayResult, error) {
		records, err := userscript.List(directory, target)
		if err != nil {
			return contract.ReplayResult{}, err
		}
		registrations := make([]contract.UserscriptRegistration, 0, len(records))
		total := 0
		for _, record := range records {
			if !record.Enabled || (id != "" && record.ID != id) {
				continue
			}
			total += len(record.Source)
			if total > 8<<20 || len(registrations) >= 512 {
				return contract.ReplayResult{}, errors.New("enabled userscripts exceed session limits")
			}
			registrations = append(registrations, contract.UserscriptRegistration{ID: record.ID, Revision: record.Revision, Source: record.Source, Matches: record.Matches, ExcludeMatches: record.ExcludeMatches})
		}
		return page.ReplayUserscripts(ctx, registrations)
	}
	latest, err := reconcile()
	if err != nil {
		return nil, err
	}
	if err := writeManagementProgress(backend.progress, extension.InstallResult{Status: "activated", Browser: backend.browserName, Profile: profile, NextAction: "Enabled stored scripts are registered in the selected page. Store updates, disables, and removals are reconciled until this command is interrupted."}); err != nil {
		return nil, err
	}
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	done, failure := sessionDisconnected(page)
	for {
		select {
		case <-ctx.Done():
			return map[string]any{"target": selected, "replay": latest, "lifetime": "connected-session"}, nil
		case <-done:
			return nil, failure()
		case <-ticker.C:
			update, err := reconcile()
			if err != nil {
				return nil, err
			}
			if update.Registered+update.Restored+update.Removed > 0 {
				latest = update
				if err := writeManagementProgress(backend.progress, extension.InstallResult{Status: "reconciled", Browser: backend.browserName, Profile: profile}); err != nil {
					return nil, err
				}
			}
		}
	}
}
