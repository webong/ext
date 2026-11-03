// Package contract defines portable browser sharing and management protocols.
// Adapters own storage and platform behavior; hosts use these types to bridge
// compatible browser resources.
package contract

import "encoding/json"

// Version is the browser share request and bundle wire version.
const Version = 2

// AvailabilityVersion is the non-secret, best-effort profile probe format.
const AvailabilityVersion = 1

type AvailabilityReport struct {
	Version    int               `json:"version"`
	Operations map[string]string `json:"operations"`
}

type Cookie struct {
	ID                int64  `json:"id,omitempty"`
	Ref               string `json:"ref,omitempty"`
	Name              string `json:"name"`
	Value             string `json:"value"`
	Domain            string `json:"domain"`
	Path              string `json:"path"`
	Expiry            int64  `json:"expiry"`
	Secure            bool   `json:"secure"`
	HTTPOnly          bool   `json:"http_only"`
	SameSitePolicy    string `json:"same_site_policy,omitempty"`
	PartitionKey      string `json:"partition_key,omitempty"`
	CrossSiteAncestor bool   `json:"has_cross_site_ancestor,omitempty"`
	// Attributes contains optional adapter-owned fields with namespaced keys.
	// Importers reject fields they cannot preserve.
	Attributes map[string]string `json:"attributes,omitempty"`
}

type CookieBundle struct {
	Version int    `json:"version"`
	Source  string `json:"source"`
	Site    string `json:"site"`
	Cookie  Cookie `json:"cookie"`
}

type CookieRequest struct {
	Version        int           `json:"version"`
	Site           string        `json:"site,omitempty"`
	Names          []string      `json:"names,omitempty"`
	IncludeExpired bool          `json:"include_expired,omitempty"`
	AllowAllHosts  bool          `json:"allow_all_hosts,omitempty"`
	Cookie         Cookie        `json:"cookie,omitempty"`
	Bundle         *CookieBundle `json:"bundle,omitempty"`
	Replace        bool          `json:"replace,omitempty"`
	// NativeExport is interpreted only by the owning adapter's normalize route.
	NativeExport json.RawMessage `json:"native_export,omitempty"`
	StoreID      string          `json:"store_id,omitempty"`
}

// CookieQueryResult contains values and non-fatal per-cookie read warnings.
type CookieQueryResult struct {
	Cookies   []Cookie `json:"cookies"`
	Warnings  []string `json:"warnings,omitempty"`
	StorePath string   `json:"store_path,omitempty"`
	StoreID   string   `json:"store_id,omitempty"`
}

type ResourceBundle struct {
	Version  int             `json:"version"`
	Resource string          `json:"resource"`
	Source   string          `json:"source"`
	Payload  json.RawMessage `json:"payload"`
}

type ResourceRequest struct {
	Version int             `json:"version"`
	Args    []string        `json:"args,omitempty"`
	Bundle  *ResourceBundle `json:"bundle,omitempty"`
	Replace bool            `json:"replace,omitempty"`
}

type PolicyEntry struct {
	Location string `json:"location"`
	Level    string `json:"level"`
	Format   string `json:"format"`
	Content  string `json:"content"`
}

type PolicyRequest struct {
	Version int `json:"version"`
}

type PolicyBundle struct {
	Version int           `json:"version"`
	Source  string        `json:"source"`
	Entries []PolicyEntry `json:"entries"`
}
