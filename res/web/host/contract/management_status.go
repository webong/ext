package contract

// Status values first-party adapters report in Response.Status and in progress
// lines. Status is descriptive: adapters may report others (at most 64
// characters), so a host must handle unknown values without failing, and must
// never infer that something is installed from a status other than
// StatusInstalled.
//
// Persistence rules a host can rely on:
//   - StatusInstalled means the adapter verified the extension in the browser
//     profile after a restart. Only extension.install may report it;
//     ValidateResponse rejects it from extension.activate.
//   - StatusActivated means the extension or script is loaded for the lifetime
//     of a browser session only; ValidateResponse rejects it from
//     extension.install.
//   - StatusAwaitingBrowserAction and StatusAwaitingBrowserConfirmation are
//     handoffs: the user must act in the browser. Nothing is installed yet.
//   - StatusRequested and StatusRequestRemoved record a registration request or
//     its removal, StatusPolicyUpdated records a policy change. None proves the
//     browser installed, blocked or removed anything.
//   - StatusPrepared, StatusStaged, StatusPackaged and StatusSigned describe
//     files; they never mean installed.
const (
	StatusReady                       = "ready"
	StatusPrepared                    = "prepared"
	StatusPackaged                    = "packaged"
	StatusStaged                      = "staged"
	StatusSigned                      = "signed"
	StatusInstalled                   = "installed"
	StatusActivated                   = "activated"
	StatusUpdated                     = "updated"
	StatusUninstalled                 = "uninstalled"
	StatusRequested                   = "requested"
	StatusRequestRemoved              = "request-removed"
	StatusPolicyUpdated               = "policy-updated"
	StatusAwaitingBrowserAction       = "awaiting-browser-action"
	StatusAwaitingBrowserConfirmation = "awaiting-browser-confirmation"
	StatusReconciled                  = "reconciled"
	StatusConnected                   = "connected"
	StatusNavigated                   = "navigated"
	StatusInjected                    = "injected"
)

// Persistent reports whether a status from an extension install or store
// operation may be presented as a verified persistent installation. Only
// StatusInstalled qualifies.
func Persistent(status string) bool { return status == StatusInstalled }
