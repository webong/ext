package contract

// PolicyEntry is one browser policy source as reported by an adapter. The
// adapter owns the native location and the interpretation of its content.
type PolicyEntry struct {
	Location string `json:"location"`
	Level    string `json:"level"`
	Format   string `json:"format"`
	Content  string `json:"content"`
}

// PolicyRequest is the complete policy.export request on standard input.
type PolicyRequest struct {
	Version int `json:"version"`
}

// PolicyBundle is the policy.export response. The host assigns Source after
// validating the adapter's response.
type PolicyBundle struct {
	Version int           `json:"version"`
	Source  string        `json:"source"`
	Entries []PolicyEntry `json:"entries"`
}
