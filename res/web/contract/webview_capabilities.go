package contract

import "fmt"

const (
	FrameMain = "main"
	FrameAll  = "all"

	RunNow           = "now"
	RunDocumentStart = "document-start"
	RunDocumentEnd   = "document-end"
	RunDocumentIdle  = "document-idle"

	WorldPage = "page"
)

// ScriptExecutionCapability describes one native WebView execution path. A
// capability declaration is descriptive; it does not authorize script source.
type ScriptExecutionCapability struct {
	Supported              bool     `json:"supported"`
	Frames                 []string `json:"frames"`
	RunAt                  []string `json:"runAt"`
	Worlds                 []string `json:"worlds"`
	RejectsStaleNavigation bool     `json:"rejectsStaleNavigation"`
}

// WebViewCapabilities separates current-page evaluation from registration in
// future documents because native WebViews often support different scopes.
type WebViewCapabilities struct {
	CurrentPage     ScriptExecutionCapability `json:"currentPage"`
	FutureDocuments ScriptExecutionCapability `json:"futureDocuments"`
}

func (c WebViewCapabilities) Validate() error {
	if err := validateScriptCapability(c.CurrentPage); err != nil {
		return fmt.Errorf("current page: %w", err)
	}
	if err := validateScriptCapability(c.FutureDocuments); err != nil {
		return fmt.Errorf("future documents: %w", err)
	}
	return nil
}

func validateScriptCapability(c ScriptExecutionCapability) error {
	if !c.Supported {
		if len(c.Frames) != 0 || len(c.RunAt) != 0 || len(c.Worlds) != 0 || c.RejectsStaleNavigation {
			return fmt.Errorf("unsupported execution must not advertise behavior")
		}
		return nil
	}
	if err := knownUnique(c.Frames, map[string]bool{FrameMain: true, FrameAll: true}); err != nil {
		return fmt.Errorf("frames: %w", err)
	}
	if err := knownUnique(c.RunAt, map[string]bool{RunNow: true, RunDocumentStart: true, RunDocumentEnd: true, RunDocumentIdle: true}); err != nil {
		return fmt.Errorf("timing: %w", err)
	}
	if err := knownUnique(c.Worlds, map[string]bool{WorldPage: true}); err != nil {
		return fmt.Errorf("worlds: %w", err)
	}
	return nil
}

func knownUnique(values []string, known map[string]bool) error {
	if len(values) == 0 {
		return fmt.Errorf("at least one value is required")
	}
	seen := map[string]bool{}
	for _, value := range values {
		if !known[value] || seen[value] {
			return fmt.Errorf("unknown or repeated value")
		}
		seen[value] = true
	}
	return nil
}
