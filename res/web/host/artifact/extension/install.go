package extension

// InstallCapability describes the routes available for a caller-owned package
// in the selected browser. Persistent installation may require browser action.
type InstallCapability struct {
	Browser                string   `json:"browser"`
	ExternalStoreRequest   bool     `json:"externalStoreRequest"`
	ExternalStoreDriver    string   `json:"externalStoreDriver,omitempty"`
	ExternalLocalPackage   bool     `json:"externalLocalPackage"`
	ExternalUpdateURL      bool     `json:"externalUpdateURL"`
	ExternalInstallScope   string   `json:"externalInstallScope,omitempty"`
	UpdateManifest         bool     `json:"updateManifest"`
	SupportedStores        []string `json:"supportedStores,omitempty"`
	ManagedPolicy          bool     `json:"managedPolicy"`
	ManagedPolicyDriver    string   `json:"managedPolicyDriver,omitempty"`
	PersistentLocalInstall bool     `json:"persistentLocalInstall"`
	InstallDriver          string   `json:"installDriver,omitempty"`
	RequiresBrowserAction  bool     `json:"requiresBrowserAction"`
	SessionLoad            bool     `json:"sessionLoad"`
	SessionDriver          string   `json:"sessionDriver,omitempty"`
	Requires               []string `json:"requires,omitempty"`
	Reason                 string   `json:"reason,omitempty"`
}

// InstallResult describes a browser-native installation handoff. A staged
// extension is not reported as installed until the browser confirms it.
type InstallResult struct {
	Status     string       `json:"status"`
	Browser    string       `json:"browser"`
	ID         string       `json:"id,omitempty"`
	Source     string       `json:"source,omitempty"`
	Profile    string       `json:"profile,omitempty"`
	NextAction string       `json:"nextAction,omitempty"`
	Extension  *Description `json:"extension,omitempty"`
}
