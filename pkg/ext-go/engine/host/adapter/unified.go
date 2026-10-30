package adapter

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	plugin "github.com/webong/ext/pkg/ext-go/engine"
	"github.com/webong/ext/pkg/ext-go/engine/package"
)

// An adapter is an ext package. Its manifest is plugin.json, the same
// ext.package/v1 document every package has, and nothing else: this file turns
// that document into the fields hosts use (Manifest) and back.
//
//   - The adapter name is the package identity id.
//   - The capabilities are the operations of the ext.adapter contract, at the
//     adapter API version; browser share and management operations are the
//     operations of two more contracts.
//   - The launchers are artifacts and entrypoints: "main" runs the executable
//     under the "argv" runtime, "main.windows" is the native Windows one.
//   - Runtime assets are further artifacts (a file, or a tree for a directory).
//   - Everything ctx itself interprets is one metadata object, MetadataKey.
//     Its schema belongs to ctx; ext never reads it (see docs/adapter-api.md).
const (
	// ManifestFileName is the manifest at the top of an adapter directory.
	ManifestFileName = "plugin.json"

	// MetadataKey names the metadata object that carries what ctx interprets.
	MetadataKey = "ctx.adapter/v1"

	// BrowserShareContract and BrowserManagementContract carry the browser
	// operations an adapter declares; BrowserContractVersion is their version.
	BrowserShareContract      = "ext.adapter.browser.share"
	BrowserManagementContract = "ext.adapter.browser.management"
	BrowserContractVersion    = "1"

	// ArgvRuntime is the entrypoint runtime of an adapter: the host invokes the
	// executable once per operation with arguments and streams, not as a
	// long-lived JSON-line session.
	ArgvRuntime = "argv"

	mainEntrypoint    = "main"
	windowsEntrypoint = "main.windows"
	mainArtifact      = "main"
	windowsArtifact   = "main-windows"
	filesArtifact     = "files-"
	maxManifestBytes  = 16 << 20
)

// Protocol is the entrypoint protocol of an adapter: its argv and stream
// binding at the adapter API version.
const Protocol = "ext.adapter/" + APIVersion

type valueKind int

const (
	kindText valueKind = iota
	kindList
	kindFlag
	kindNumber
)

// metadataKinds lists the keys of the ctx.adapter/v1 object and how each is
// typed in JSON. The names are the keys of the compact key = "value" notation
// earlier versions used, so one name means one thing.
var metadataKinds = map[string]valueKind{
	"display_name": kindText, "description": kindText, "runtime": kindText,
	"selector_key": kindText, "computer_hook_settings_file": kindText,
	"computer_hook_template": kindText, "go_entry": kindText,
	"surfaces": kindList, "supports": kindList, "engine": kindList, "role": kindList, "extra_keys": kindList,
	"commands": kindList, "computer_commands": kindList,
	"computer_capabilities": kindList, "computer_hook_events": kindList,
	"computer_hook_default_events": kindList, "share_spaces": kindList,
	"override_env": kindList, "dependencies_darwin": kindList,
	"dependencies_linux": kindList, "dependencies_windows": kindList,
	"selectable": kindFlag, "default_provider": kindFlag,
	"self_contained": kindFlag, "browser_query_auto": kindFlag,
	"browser_query_priority": kindNumber,
}

// structuralKeys are the keys the package document expresses without metadata.
var structuralKeys = map[string]bool{
	"api_version": true, "name": true, "capabilities": true, "browser_share": true,
	"browser_management": true, "executable": true, "executable_windows": true,
	"package_files": true,
}

// Target is the platform a package is built for. A zero Target is portable.
type Target struct{ OS, Arch string }

// readPackage reads and decodes the manifest of an adapter directory. A source
// directory holds a template (artifact digests and the revision are empty); an
// installed or packaged one holds the sealed manifest. Both are returned as read.
func readPackage(directory string) (packagekit.Manifest, error) {
	var pm packagekit.Manifest
	path := filepath.Join(directory, ManifestFileName)
	info, err := os.Lstat(path)
	if err != nil {
		if _, old := os.Lstat(filepath.Join(directory, "adapter.toml")); old == nil {
			return pm, fmt.Errorf("adapter manifest not found: %s (adapter.toml is no longer read; an adapter is described by %s)", path, ManifestFileName)
		}
		return pm, fmt.Errorf("adapter manifest not found: %s", path)
	}
	if !info.Mode().IsRegular() {
		return pm, fmt.Errorf("adapter manifest %s is not a regular file", path)
	}
	file, err := os.Open(path)
	if err != nil {
		return pm, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxManifestBytes+1))
	if err != nil {
		return pm, err
	}
	if len(data) > maxManifestBytes {
		return pm, fmt.Errorf("adapter manifest %s exceeds 16 MiB", path)
	}
	if err := plugin.Decode(data, &pm); err != nil {
		return pm, fmt.Errorf("adapter manifest %s: %w", path, err)
	}
	if err := validateTemplate(pm); err != nil {
		return pm, fmt.Errorf("adapter manifest %s: %w", path, err)
	}
	return pm, nil
}

// sealed reports whether the manifest carries digests. A template carries none;
// a manifest with some but not all is neither and is rejected by validateTemplate.
func sealed(pm packagekit.Manifest) bool {
	return len(pm.Artifacts) > 0 && pm.Artifacts[0].SHA256 != ""
}

// validateTemplate applies the full ext.package/v1 validation to a manifest whose
// digests may still be empty, by validating it with placeholders. A manifest
// is either wholly a template or wholly sealed.
func validateTemplate(pm packagekit.Manifest) error {
	filled := 0
	for _, artifact := range pm.Artifacts {
		if artifact.SHA256 != "" {
			filled++
		}
	}
	isSealed := filled > 0
	if filled != 0 && filled != len(pm.Artifacts) {
		return errors.New("some artifacts have a digest and some do not")
	}
	if isSealed != (pm.Descriptor.Identity.Revision != "") {
		return errors.New("the revision and the artifact digests must be set together")
	}
	if isSealed {
		return pm.Validate()
	}
	placeholder := pm
	placeholder.Artifacts = append([]packagekit.Artifact(nil), pm.Artifacts...)
	for i := range placeholder.Artifacts {
		placeholder.Artifacts[i].SHA256 = strings.Repeat("0", 64)
	}
	placeholder.Descriptor.Identity.Revision = "template"
	return placeholder.Validate()
}

// valuesFromPackage returns the compact key/value form of an adapter manifest,
// the input of manifestFromValues, from a package document.
func valuesFromPackage(pm packagekit.Manifest) (map[string]string, error) {
	values := map[string]string{"name": pm.Descriptor.Identity.ID}
	var adapterContracts int
	for _, contract := range pm.Descriptor.Contracts {
		operations := make([]string, 0, len(contract.Operations))
		for _, operation := range contract.Operations {
			operations = append(operations, operation.Name)
		}
		switch {
		case contract.Name == plugin.AdapterContractName:
			adapterContracts++
			values["api_version"] = contract.Version
			values["capabilities"] = strings.Join(operations, ",")
		case contract.Name == BrowserShareContract && contract.Version == BrowserContractVersion:
			values["browser_share"] = strings.Join(operations, ",")
		case contract.Name == BrowserManagementContract && contract.Version == BrowserContractVersion:
			values["browser_management"] = strings.Join(operations, ",")
		default:
			return nil, fmt.Errorf("adapter declares contract %s@%s, which ctx does not define for adapters", contract.Name, contract.Version)
		}
	}
	if adapterContracts != 1 {
		return nil, fmt.Errorf("adapter must declare exactly one %s contract", plugin.AdapterContractName)
	}
	artifacts := map[string]packagekit.Artifact{}
	for _, artifact := range pm.Artifacts {
		artifacts[artifact.Name] = artifact
	}
	launched := map[string]bool{}
	for _, entry := range pm.Entrypoints {
		if entry.Runtime != ArgvRuntime || len(entry.Protocols) != 1 || entry.Protocols[0] != Protocol {
			return nil, fmt.Errorf("entrypoint %s must use the %s runtime and the %s protocol", entry.Name, ArgvRuntime, Protocol)
		}
		launched[entry.Artifact] = true
		switch entry.Name {
		case mainEntrypoint:
			values["executable"] = artifacts[entry.Artifact].Path
		case windowsEntrypoint:
			values["executable_windows"] = artifacts[entry.Artifact].Path
		default:
			return nil, fmt.Errorf("unknown entrypoint %s (expected %s or %s)", entry.Name, mainEntrypoint, windowsEntrypoint)
		}
	}
	if values["executable"] == "" {
		// a package that targets only Windows: its one launcher is also the default
		values["executable"] = values["executable_windows"]
	}
	var files []string
	for _, artifact := range pm.Artifacts {
		if !launched[artifact.Name] {
			files = append(files, artifact.Path)
		}
	}
	if len(files) > 0 {
		values["package_files"] = strings.Join(files, ",")
	}
	if raw, ok := pm.Metadata[MetadataKey]; ok {
		if err := metadataValues(raw, values); err != nil {
			return nil, err
		}
	}
	return values, nil
}

// metadataValues reads the ctx.adapter/v1 object into the key/value form.
func metadataValues(raw json.RawMessage, values map[string]string) error {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return fmt.Errorf("metadata %s: %w", MetadataKey, err)
	}
	for key, value := range object {
		kind, known := metadataKinds[key]
		if !known {
			return fmt.Errorf("metadata %s has unknown key %q", MetadataKey, key)
		}
		switch kind {
		case kindText:
			var text string
			if err := json.Unmarshal(value, &text); err != nil {
				return fmt.Errorf("metadata %s: %s must be a string", MetadataKey, key)
			}
			values[key] = text
		case kindList:
			var list []string
			if err := json.Unmarshal(value, &list); err != nil {
				return fmt.Errorf("metadata %s: %s must be a list of strings", MetadataKey, key)
			}
			for _, item := range list {
				if item == "" || strings.ContainsAny(item, ",\n") || strings.TrimSpace(item) != item {
					return fmt.Errorf("metadata %s: %s has an invalid entry %q", MetadataKey, key, item)
				}
			}
			values[key] = strings.Join(list, ",")
		case kindFlag:
			var flag bool
			if err := json.Unmarshal(value, &flag); err != nil {
				return fmt.Errorf("metadata %s: %s must be true or false", MetadataKey, key)
			}
			values[key] = strconv.FormatBool(flag)
		case kindNumber:
			var number int
			if err := json.Unmarshal(value, &number); err != nil {
				return fmt.Errorf("metadata %s: %s must be an integer", MetadataKey, key)
			}
			values[key] = strconv.Itoa(number)
		}
	}
	return nil
}

// templateFromValues builds the package template of an adapter from its compact
// key/value form. It is how the sources in this repository were converted and how
// tests write adapters; the loader never reads that form.
func templateFromValues(values map[string]string) (packagekit.Manifest, error) {
	var pm packagekit.Manifest
	for key := range values {
		if _, meta := metadataKinds[key]; !meta && !structuralKeys[key] && key != "kind" {
			return pm, fmt.Errorf("unknown manifest key %q", key)
		}
	}
	if values["kind"] != "" {
		return pm, errors.New("the kind key was removed; declare runtime and surfaces")
	}
	if values["api_version"] != APIVersion {
		return pm, fmt.Errorf("adapter API %q is not supported (expected %s)", values["api_version"], APIVersion)
	}
	operations := func(list []string) []plugin.Operation {
		out := make([]plugin.Operation, 0, len(list))
		for _, name := range list {
			out = append(out, plugin.Operation{Name: name})
		}
		return out
	}
	contracts := []plugin.Contract{{
		ContractRef: plugin.ContractRef{Name: plugin.AdapterContractName, Version: APIVersion},
		Operations:  operations(splitList(values["capabilities"])),
	}}
	if share := splitList(values["browser_share"]); len(share) > 0 {
		contracts = append(contracts, plugin.Contract{ContractRef: plugin.ContractRef{Name: BrowserShareContract, Version: BrowserContractVersion}, Operations: operations(share)})
	}
	if management := splitList(values["browser_management"]); len(management) > 0 {
		contracts = append(contracts, plugin.Contract{ContractRef: plugin.ContractRef{Name: BrowserManagementContract, Version: BrowserContractVersion}, Operations: operations(management)})
	}
	pm = packagekit.Manifest{
		APIVersion: packagekit.Version,
		Descriptor: plugin.Descriptor{APIVersion: plugin.APIVersion, Identity: plugin.Identity{ID: values["name"]}, Contracts: contracts},
	}
	if executable := values["executable"]; executable != "" {
		pm.Artifacts = append(pm.Artifacts, packagekit.Artifact{Name: mainArtifact, Path: executable})
		pm.Entrypoints = append(pm.Entrypoints, packagekit.Entrypoint{Name: mainEntrypoint, Runtime: ArgvRuntime, Artifact: mainArtifact, Protocols: []string{Protocol}})
	}
	if windows := values["executable_windows"]; windows != "" {
		pm.Artifacts = append(pm.Artifacts, packagekit.Artifact{Name: windowsArtifact, Path: windows})
		pm.Entrypoints = append(pm.Entrypoints, packagekit.Entrypoint{Name: windowsEntrypoint, Runtime: ArgvRuntime, Artifact: windowsArtifact, Protocols: []string{Protocol}})
	}
	for index, file := range splitList(values["package_files"]) {
		pm.Artifacts = append(pm.Artifacts, packagekit.Artifact{Name: filesArtifact + strconv.Itoa(index+1), Path: file})
	}
	object := map[string]any{}
	for key, kind := range metadataKinds {
		value, present := values[key]
		if !present || value == "" {
			continue
		}
		switch kind {
		case kindText:
			object[key] = value
		case kindList:
			if list := splitList(value); len(list) > 0 {
				object[key] = list
			}
		case kindFlag:
			if value != "true" && value != "false" {
				return pm, fmt.Errorf("%s must be true or false, not %q", key, value)
			}
			object[key] = value == "true"
		case kindNumber:
			number, err := strconv.Atoi(value)
			if err != nil {
				return pm, fmt.Errorf("%s must be an integer, not %q", key, value)
			}
			object[key] = number
		}
	}
	if len(object) > 0 {
		encoded, err := json.Marshal(object)
		if err != nil {
			return pm, err
		}
		pm.Metadata = map[string]json.RawMessage{MetadataKey: encoded}
	}
	return pm, nil
}

// TemplateFromKeyValues converts the compact key = "value" notation of earlier
// ext versions into a package template. It exists for tooling and for tests that
// write adapters; the loader reads plugin.json only.
func TemplateFromKeyValues(text string) (packagekit.Manifest, error) {
	values, err := parseKeyValues(strings.NewReader(text))
	if err != nil {
		return packagekit.Manifest{}, err
	}
	return templateFromValues(values)
}

// WriteKeyValueManifest writes the plugin.json template of an adapter given in
// the compact key = "value" notation to directory.
func WriteKeyValueManifest(directory, text string) error {
	pm, err := TemplateFromKeyValues(text)
	if err != nil {
		return err
	}
	return WriteManifest(directory, pm)
}

// WriteManifest writes pm as the plugin.json of directory.
func WriteManifest(directory string, pm packagekit.Manifest) error {
	if err := validateTemplate(pm); err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(pm, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(directory, ManifestFileName), append(encoded, '\n'), 0o644)
}

func parseKeyValues(input io.Reader) (map[string]string, error) {
	values := map[string]string{}
	scanner := bufio.NewScanner(input)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		value, err := strconv.Unquote(strings.TrimSpace(parts[1]))
		if err != nil {
			continue
		}
		values[strings.TrimSpace(parts[0])] = value
	}
	return values, scanner.Err()
}

// Seal turns the template in directory into a sealed manifest and writes it
// as plugin.json. The artifacts the template declares must exist; every other
// regular file in the directory is added as an artifact too, in path order, so the
// sealed manifest lists exactly the files the directory holds and a first-party
// adapter keeps shipping the helper binaries built beside its launcher. Seal
// computes each digest (a declared directory is a tree artifact), stamps the
// platform on the launchers when target is set, and sets the revision to the
// digest of the first launcher. For a target it first drops the launcher of the
// other kind (see pruneToTarget); the dropped launcher's file is not listed.
func Seal(directory string, target Target) (packagekit.Manifest, error) {
	pm, err := readPackage(directory)
	if err != nil {
		return pm, err
	}
	if sealed(pm) {
		return pm, errors.New("adapter manifest is already sealed")
	}
	pm, dropped := pruneToTarget(pm, target)
	launchers := map[string]bool{}
	for _, entry := range pm.Entrypoints {
		launchers[entry.Artifact] = true
	}
	declared := map[string]bool{strings.ToLower(ManifestFileName): true}
	var trees []string
	for i := range pm.Artifacts {
		artifact := &pm.Artifacts[i]
		declared[strings.ToLower(artifact.Path)] = true
		path := filepath.Join(directory, filepath.FromSlash(artifact.Path))
		info, err := os.Lstat(path)
		if err != nil {
			return pm, fmt.Errorf("artifact %s: %w", artifact.Name, err)
		}
		switch {
		case info.IsDir():
			artifact.Kind = packagekit.ArtifactTree
			trees = append(trees, strings.ToLower(artifact.Path)+"/")
			artifact.SHA256, err = packagekit.TreeDigest(directory, artifact.Path)
		case info.Mode().IsRegular():
			artifact.SHA256, err = packagekit.FileDigest(path)
		default:
			err = errors.New("not a regular file or directory")
		}
		if err != nil {
			return pm, fmt.Errorf("artifact %s: %w", artifact.Name, err)
		}
		if launchers[artifact.Name] {
			artifact.OS, artifact.Arch = target.OS, target.Arch
		}
	}
	var extras []string
	err = filepath.WalkDir(directory, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("%s is not a regular file", path)
		}
		relative, err := filepath.Rel(directory, path)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(relative)
		lower := strings.ToLower(name)
		if declared[lower] || lower == strings.ToLower(dropped) {
			return nil
		}
		for _, tree := range trees {
			if strings.HasPrefix(lower, tree) {
				return nil
			}
		}
		extras = append(extras, name)
		return nil
	})
	if err != nil {
		return pm, err
	}
	sort.Strings(extras)
	for index, name := range extras {
		digest, err := packagekit.FileDigest(filepath.Join(directory, filepath.FromSlash(name)))
		if err != nil {
			return pm, fmt.Errorf("file %s: %w", name, err)
		}
		pm.Artifacts = append(pm.Artifacts, packagekit.Artifact{Name: "extra-" + strconv.Itoa(index+1), Path: name, SHA256: digest})
	}
	if len(pm.Entrypoints) == 0 {
		return pm, errors.New("adapter declares no executable")
	}
	for _, artifact := range pm.Artifacts {
		if artifact.Name == pm.Entrypoints[0].Artifact {
			pm.Descriptor.Identity.Revision = "sha256:" + artifact.SHA256
		}
	}
	if err := pm.Validate(); err != nil {
		return pm, err
	}
	return pm, WriteManifest(directory, pm)
}

// pruneToTarget drops the launcher that does not apply to target and returns the
// path of its file, which the caller must not ship. A Windows target keeps
// "main.windows" when the template has one; any other target keeps "main". A
// zero target keeps both.
func pruneToTarget(pm packagekit.Manifest, target Target) (packagekit.Manifest, string) {
	if target.OS == "" {
		return pm, ""
	}
	drop := windowsEntrypoint
	if target.OS == "windows" && hasEntrypoint(pm, windowsEntrypoint) {
		drop = mainEntrypoint
	}
	var droppedArtifact string
	entrypoints := pm.Entrypoints[:0:0]
	for _, entry := range pm.Entrypoints {
		if entry.Name == drop {
			droppedArtifact = entry.Artifact
			continue
		}
		entrypoints = append(entrypoints, entry)
	}
	if droppedArtifact == "" {
		return pm, ""
	}
	var droppedPath string
	artifacts := pm.Artifacts[:0:0]
	for _, artifact := range pm.Artifacts {
		if artifact.Name == droppedArtifact {
			droppedPath = artifact.Path
			continue
		}
		artifacts = append(artifacts, artifact)
	}
	pm.Entrypoints, pm.Artifacts = entrypoints, artifacts
	return pm, droppedPath
}

func hasEntrypoint(pm packagekit.Manifest, name string) bool {
	for _, entry := range pm.Entrypoints {
		if entry.Name == name {
			return true
		}
	}
	return false
}

// verifyPackage checks an installed or packaged adapter against its sealed
// manifest: every listed artifact has its digest, and the directory holds
// nothing else. It does not run anything.
func verifyPackage(directory string, pm packagekit.Manifest) error {
	if !sealed(pm) {
		return errors.New("adapter manifest is not sealed")
	}
	listed := map[string]bool{ManifestFileName: true}
	trees := map[string]bool{}
	for _, artifact := range pm.Artifacts {
		if artifact.Kind == packagekit.ArtifactTree {
			trees[strings.ToLower(artifact.Path)] = true
		} else {
			listed[strings.ToLower(artifact.Path)] = true
		}
	}
	if err := pm.VerifyArtifacts(directory); err != nil {
		return err
	}
	var extra []string
	err := filepath.WalkDir(directory, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}
		relative, err := filepath.Rel(directory, path)
		if err != nil {
			return err
		}
		name := strings.ToLower(filepath.ToSlash(relative))
		if listed[name] {
			return nil
		}
		for tree := range trees {
			if strings.HasPrefix(name, tree+"/") {
				return nil
			}
		}
		extra = append(extra, filepath.ToSlash(relative))
		return nil
	})
	if err != nil {
		return err
	}
	if len(extra) > 0 {
		sort.Strings(extra)
		return fmt.Errorf("%w: adapter directory holds files its manifest does not list: %s", plugin.ErrInvalid, strings.Join(extra, ", "))
	}
	return nil
}
