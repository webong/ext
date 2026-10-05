package adapterkit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"

	"github.com/webong/ctx/res/browser/bookmarklet"
	"github.com/webong/ctx/res/browser/extension"
	"github.com/webong/ctx/res/browser/userscript"
	"github.com/webong/ctx/internal/app/browser/management"
)

// NativeExtensionBackend is implemented by the selected adapter. The shared
// manager has no native browser dispatch or fallback installation route.
type NativeExtensionBackend interface {
	ManageExtension(context.Context, string, string, json.RawMessage, func(extension.InstallResult) error) (any, string, error)
}

// ExtensionInspector optionally inspects a provider-native package format.
type ExtensionInspector interface {
	InspectExtension(context.Context, string) (extension.Description, error)
}

// UserscriptSessionBackend owns page execution for the selected adapter.
type UserscriptSessionBackend interface {
	ActivateUserscript(context.Context, string, userscript.Record, func(extension.InstallResult) error) (any, error)
}

// UserscriptTargetSessionBackend additionally receives opaque native target
// options, allowing the adapter to bind an explicit executable/profile root.
type UserscriptTargetSessionBackend interface {
	ActivateUserscriptTarget(context.Context, string, userscript.Record, json.RawMessage, func(extension.InstallResult) error) (any, error)
}

// RunLocalManagement combines portable workflows with an adapter's native backend.
func RunLocalManagement(ctx context.Context, browserName, profile string, input io.Reader, stdout, stderr io.Writer, native NativeExtensionBackend) int {
	if ctx == nil {
		var stop context.CancelFunc
		ctx, stop = signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()
	}
	return RunManagement(ctx, profile, input, stdout, stderr, localManagementBackend{browserName: browserName, progress: stderr, native: native})
}

type localManagementBackend struct {
	browserName string
	progress    io.Writer
	native      NativeExtensionBackend
}

func (backend localManagementBackend) ManageBrowser(ctx context.Context, profile string, request management.Request) (management.Response, error) {
	var result any
	status := "prepared"
	switch request.Kind {
	case "extension":
		var err error
		result, status, err = backend.manageExtension(ctx, profile, request.Action, request.Input)
		if err != nil {
			return management.Response{}, err
		}
	case "userscript":
		var err error
		result, status, err = backend.manageUserscript(ctx, profile, request.Action, request.Input)
		if err != nil {
			return management.Response{}, err
		}
	case "session":
		var err error
		result, status, err = backend.manageSession(ctx, profile, request.Action, request.Input)
		if err != nil {
			return management.Response{}, err
		}
	case "bookmarklet":
		var err error
		result, status, err = manageBookmarklet(request.Action, request.Input)
		if err != nil {
			return management.Response{}, err
		}
	default:
		return management.Response{}, fmt.Errorf("unsupported browser management kind %q", request.Kind)
	}
	data, err := json.Marshal(result)
	if err != nil {
		return management.Response{}, err
	}
	return management.Response{Version: management.Version, Kind: request.Kind, Action: request.Action, Status: status, Result: data}, nil
}

type extensionInput struct {
	Source   string `json:"source"`
	Output   string `json:"output"`
	Dest     string `json:"destination"`
	Revision string `json:"revision"`
}

func (backend localManagementBackend) manageExtension(ctx context.Context, profile, action string, raw json.RawMessage) (any, string, error) {
	var input extensionInput
	if action == "prepare" || action == "package" || action == "stage" {
		if err := decodeManagementInput(raw, &input); err != nil {
			return nil, "", err
		}
	}
	switch action {
	case "prepare":
		if inspector, ok := backend.native.(ExtensionInspector); ok {
			prepared, err := inspector.InspectExtension(ctx, input.Source)
			return prepared, "prepared", err
		}
		prepared, err := extension.Inspect(input.Source)
		return prepared, "prepared", err
	case "package":
		prepared, err := extension.Package(input.Source, input.Output)
		return map[string]any{"description": prepared, "path": input.Output}, "packaged", err
	case "stage":
		prepared, err := extension.StageWithRevision(input.Source, input.Dest, input.Revision)
		return map[string]any{"description": prepared, "path": input.Dest}, "staged", err
	default:
		if backend.native == nil {
			return nil, "", fmt.Errorf("this adapter has no native extension backend for %q", action)
		}
		return backend.native.ManageExtension(ctx, profile, action, raw, func(result extension.InstallResult) error {
			return writeManagementProgress(backend.progress, result)
		})
	}
}

func writeManagementProgress(output io.Writer, result extension.InstallResult) error {
	if output == nil {
		return nil
	}
	if _, err := fmt.Fprint(output, "browser management progress: "); err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(result)
}

type userscriptInput struct {
	ID             string                   `json:"id"`
	Name           string                   `json:"name"`
	Matches        []string                 `json:"matches"`
	ExcludeMatches []string                 `json:"excludeMatches,omitempty"`
	Source         string                   `json:"source,omitempty"`
	SourceFile     string                   `json:"sourceFile,omitempty"`
	Revision       string                   `json:"revision,omitempty"`
	SessionTarget  management.SessionTarget `json:"sessionTarget,omitempty"`
}

func (backend localManagementBackend) manageUserscript(ctx context.Context, profile, action string, raw json.RawMessage) (any, string, error) {
	directory, err := userscript.DefaultDirectory()
	if err != nil {
		return nil, "", err
	}
	target := backend.browserName + ":" + profile
	var input userscriptInput
	if err := decodeManagementInput(raw, &input); err != nil {
		return nil, "", err
	}
	switch action {
	case "activate":
		if input.SessionTarget.ID != "" || input.SessionTarget.Endpoint != "" || input.SessionTarget.Protocol != "" {
			result, err := backend.activateStoredUserscripts(ctx, profile, directory, target, input.ID, input.SessionTarget, raw)
			return result, "activated", err
		}
		session, ok := backend.native.(UserscriptSessionBackend)
		targetSession, hasTargetSession := backend.native.(UserscriptTargetSessionBackend)
		if !ok && !hasTargetSession {
			return nil, "", fmt.Errorf("userscript session activation is unavailable for browser %q", backend.browserName)
		}
		record, err := userscript.Load(directory, target, input.ID)
		if err != nil {
			return nil, "", err
		}
		if !record.Enabled {
			return nil, "", fmt.Errorf("userscript %q is disabled", record.ID)
		}
		progress := func(result extension.InstallResult) error {
			return writeManagementProgress(backend.progress, result)
		}
		var result any
		if hasTargetSession {
			result, err = targetSession.ActivateUserscriptTarget(ctx, profile, record, raw, progress)
		} else {
			result, err = session.ActivateUserscript(ctx, profile, record, progress)
		}
		return result, "activated", err
	case "prepare", "install", "update":
		if input.SourceFile != "" {
			if input.Source != "" {
				return nil, "", errors.New("choose source or sourceFile")
			}
			file, err := os.Open(input.SourceFile)
			if err != nil {
				return nil, "", err
			}
			data, readErr := io.ReadAll(io.LimitReader(file, 1<<20+1))
			closeErr := file.Close()
			if readErr != nil || closeErr != nil {
				return nil, "", errors.Join(readErr, closeErr)
			}
			input.Source = string(data)
		}
		record := userscript.Record{Target: target, ID: input.ID, Name: input.Name, Matches: input.Matches,
			ExcludeMatches: input.ExcludeMatches, Enabled: true, Source: input.Source}
		record = userscript.InferMetadata(record, input.SourceFile)
		record.Revision = userscript.Revision(record)
		if input.Revision != "" && input.Revision != record.Revision {
			return nil, "", errors.New("userscript changed since preparation")
		}
		if err := userscript.Validate(record); err != nil {
			return nil, "", err
		}
		if action == "prepare" {
			description := record.Description()
			description.Status = "prepared"
			return description, "prepared", nil
		}
		replace := action == "update"
		if replace {
			previous, err := userscript.Load(directory, target, record.ID)
			if err != nil {
				return nil, "", err
			}
			record.Enabled = previous.Enabled
		}
		if err := userscript.Save(directory, record, replace); err != nil {
			return nil, "", err
		}
		description := record.Description()
		description.Status = "installed"
		if action == "update" {
			description.Status = "updated"
		}
		return description, description.Status, nil
	case "list":
		records, err := userscript.List(directory, target)
		if err != nil {
			return nil, "", err
		}
		descriptions := make([]userscript.Description, 0, len(records))
		for _, record := range records {
			descriptions = append(descriptions, record.Description())
		}
		return descriptions, "ready", nil
	case "describe":
		record, err := userscript.Load(directory, target, input.ID)
		if err != nil {
			return nil, "", err
		}
		return record.Description(), "ready", nil
	case "enable", "disable":
		record, err := userscript.SetEnabled(directory, target, input.ID, action == "enable")
		if err != nil {
			return nil, "", err
		}
		return record.Description(), action + "d", nil
	case "uninstall":
		if err := userscript.Remove(directory, target, input.ID); err != nil {
			return nil, "", err
		}
		return map[string]string{"id": input.ID, "target": target}, "uninstalled", nil
	default:
		return nil, "", fmt.Errorf("unsupported userscript operation %q", action)
	}
}

type bookmarkletInput struct {
	Name       string `json:"name,omitempty"`
	Source     string `json:"source,omitempty"`
	SourceFile string `json:"sourceFile,omitempty"`
	URL        string `json:"url,omitempty"`
	Output     string `json:"output,omitempty"`
}

func manageBookmarklet(action string, raw json.RawMessage) (any, string, error) {
	var input bookmarkletInput
	if err := decodeManagementInput(raw, &input); err != nil {
		return nil, "", err
	}
	if input.SourceFile != "" {
		if input.Source != "" {
			return nil, "", errors.New("choose source or sourceFile")
		}
		file, err := os.Open(input.SourceFile)
		if err != nil {
			return nil, "", err
		}
		data, readErr := io.ReadAll(io.LimitReader(file, bookmarklet.MaxSourceBytes+1))
		closeErr := file.Close()
		if readErr != nil || closeErr != nil {
			return nil, "", errors.Join(readErr, closeErr)
		}
		input.Source = string(data)
	}
	switch action {
	case "encode":
		value, err := bookmarklet.Encode(input.Source)
		return map[string]string{"url": value}, "prepared", err
	case "decode":
		source, err := bookmarklet.Decode(input.URL)
		return map[string]string{"source": source}, "prepared", err
	case "install_page":
		page, err := bookmarklet.InstallPage(input.Name, input.Source)
		if err != nil {
			return nil, "", err
		}
		if input.Output == "" {
			return map[string]string{"html": page}, "prepared", nil
		}
		if !filepath.IsAbs(input.Output) {
			return nil, "", errors.New("bookmarklet output path must be absolute")
		}
		file, err := os.OpenFile(input.Output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return nil, "", err
		}
		_, writeErr := io.WriteString(file, page)
		closeErr := file.Close()
		if writeErr != nil || closeErr != nil {
			_ = os.Remove(input.Output)
			return nil, "", errors.Join(writeErr, closeErr)
		}
		return map[string]string{"path": input.Output}, "prepared", nil
	default:
		return nil, "", fmt.Errorf("unsupported bookmarklet operation %q", action)
	}
}

func decodeManagementInput(raw json.RawMessage, target any) error {
	if len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return fmt.Errorf("invalid management input: %w", err)
	}
	return nil
}
