package chromium

import (
	"encoding/json"
	"encoding/xml"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/webong/ext/res/web/extension"
)

// UpdateManifestResult is a publishing artifact, never an installation result.
// Hosts serve XML as application/xml and CRX as application/x-chrome-extension.
type UpdateManifestResult struct {
	Status           string `json:"status"`
	ID               string `json:"id"`
	Version          string `json:"version"`
	ArtifactRevision string `json:"artifactRevision"`
	CodebaseURL      string `json:"codebaseURL"`
	Path             string `json:"path,omitempty"`
	XML              string `json:"xml,omitempty"`
}

// CRXUpdateManifest generates Chrome's native update XML from a reviewed CRX3.
// It does not contact the URL, upload an artifact, or publish signing keys.
func CRXUpdateManifest(source, revision, codebase, output string) (UpdateManifestResult, error) {
	description, err := checkedCRX(source, revision, "", "")
	if err != nil {
		return UpdateManifestResult{}, err
	}
	if _, err := distributionURL(codebase); err != nil {
		return UpdateManifestResult{}, err
	}
	document := struct {
		XMLName   xml.Name `xml:"gupdate"`
		Namespace string   `xml:"xmlns,attr"`
		Protocol  string   `xml:"protocol,attr"`
		App       struct {
			ID     string `xml:"appid,attr"`
			Update struct {
				Codebase string `xml:"codebase,attr"`
				Version  string `xml:"version,attr"`
			} `xml:"updatecheck"`
		} `xml:"app"`
	}{Namespace: "http://www.google.com/update2/response", Protocol: "2.0"}
	document.App.ID = description.ID
	document.App.Update.Codebase = codebase
	document.App.Update.Version = description.Version
	data, err := xml.MarshalIndent(document, "", "  ")
	if err != nil {
		return UpdateManifestResult{}, err
	}
	data = append(append([]byte(xml.Header), data...), '\n')
	result := UpdateManifestResult{Status: "prepared", ID: description.ID, Version: description.Version, ArtifactRevision: description.Revision, CodebaseURL: codebase}
	if output == "" {
		result.XML = string(data)
		return result, nil
	}
	if err := extension.UnusedArtifact(output, ".xml"); err != nil {
		return UpdateManifestResult{}, err
	}
	file, err := os.OpenFile(output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return UpdateManifestResult{}, err
	}
	_, writeErr := file.Write(data)
	closeErr := file.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		os.Remove(output)
		return UpdateManifestResult{}, err
	}
	result.Path = output
	return result, nil
}

func distributionURL(value string) (*url.URL, error) {
	parsed, err := url.Parse(value)
	if err != nil || len(value) > 4096 || strings.ContainsAny(value, "\r\n\x00") || parsed.User != nil || parsed.Hostname() == "" || parsed.Fragment != "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return nil, errors.New("extension distribution URL must be an absolute HTTP(S) URL without credentials or a fragment")
	}
	return parsed, nil
}

func changeDistributionInstall(goos, browser string, config StoreConfig, input extensionInput, remove bool) (extension.InstallResult, error) {
	if input.Source == "" && input.UpdateURL == "" {
		if input.Revision != "" || input.Version != "" {
			return extension.InstallResult{}, errors.New("artifact revision and version require a local CRX source")
		}
		return changeStoreInstall(goos, browser, config, input.Store, input.ID, input.ExternalDirectory, remove)
	}
	if input.Source != "" && input.UpdateURL != "" || input.Store != "" {
		return extension.InstallResult{}, errors.New("choose one installation source: named store, updateURL, or local CRX source")
	}
	if goos != "linux" {
		return extension.InstallResult{}, errors.New("this adapter permits custom update servers and local CRX registration only on Linux; use a named store or manually Load unpacked source on this platform")
	}
	properties := map[string]string{}
	id := input.ID
	var description *extension.Description
	if input.UpdateURL != "" {
		if !config.LinuxUpdateURL {
			return extension.InstallResult{}, errors.New("this adapter has no custom update-server registration route")
		}
		if input.Revision != "" || input.Version != "" {
			return extension.InstallResult{}, errors.New("artifact revision and version cannot be combined with updateURL")
		}
		if _, err := distributionURL(input.UpdateURL); err != nil {
			return extension.InstallResult{}, err
		}
		properties["external_update_url"] = input.UpdateURL
	} else {
		if !config.LinuxLocalCRX {
			return extension.InstallResult{}, errors.New("this adapter has no local CRX registration route")
		}
		if !filepath.IsAbs(input.Source) || !strings.EqualFold(filepath.Ext(input.Source), ".crx") {
			return extension.InstallResult{}, errors.New("local registration requires an absolute CRX path")
		}
		version := input.Version
		if !remove {
			prepared, err := checkedCRX(input.Source, input.Revision, id, version)
			if err != nil {
				return extension.InstallResult{}, err
			}
			description = &prepared
			id = prepared.ID
			version = prepared.Version
		} else if !validCRXVersion(version) {
			// Removal must work even when the publisher has already deleted the CRX.
			return extension.InstallResult{}, errors.New("local request removal requires its original extension ID, source path, and version")
		}
		properties["external_crx"] = filepath.Clean(input.Source)
		properties["external_version"] = version
	}
	if !chromiumExtensionID.MatchString(id) {
		return extension.InstallResult{}, errors.New("a 32-character extension ID using a-p is required")
	}
	source, err := changeUnixExternalProperties(goos, browser, config, id, properties, input.ExternalDirectory, remove)
	if err != nil {
		return extension.InstallResult{}, err
	}
	if remove {
		return extension.InstallResult{Status: "request-removed", Browser: browser, ID: id, Source: source, NextAction: "Restart the browser and check its extensions page. Removing a request does not remove an independently installed extension."}, nil
	}
	next := "Restart the browser and verify the extension in its extensions page."
	if description != nil {
		next += " Keep the registered CRX path available and readable. Updating a local package also requires updating the registration's version."
	}
	return extension.InstallResult{Status: "requested", Browser: browser, ID: id, Source: source, Extension: description, NextAction: next}, nil
}

func sameExternalProperties(actual, expected map[string]string) bool {
	if len(actual) != len(expected) {
		return false
	}
	for key, value := range expected {
		if actual[key] != value {
			return false
		}
	}
	return true
}

// Verify that the registration contents are the selected request, not a
// provider preference with additional policy or native scope metadata.
func decodeExternalProperties(data []byte, expected map[string]string) error {
	var actual map[string]string
	if jsonErr := json.Unmarshal(data, &actual); jsonErr != nil || !sameExternalProperties(actual, expected) {
		return errors.New("external request differs from the selected request; refusing to remove it")
	}
	return nil
}
