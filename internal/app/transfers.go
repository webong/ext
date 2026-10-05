package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/webong/ctx/internal/config"
	modpkg "github.com/webong/ctx/internal/mod"
	systemgraph "github.com/webong/ctx/pkg/graph/system"
)

type endpoint struct {
	Provider *modpkg.Adapter
	Name     string
	Instance *managerInstance
}

func buildImage(resolver *config.Resolver, args []string, stdout, stderr io.Writer) int {
	providerName := ""
	var named *endpoint
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		if strings.HasPrefix(args[0], "@") {
			selected, err := parseEndpoint(args[0])
			if err != nil {
				return reportErrorCode(stderr, err, 2)
			}
			named, args = &selected, args[1:]
		} else if candidate, err := managerProvider(args[0]); err == nil {
			providerName, args = candidate.Manifest.Name, args[1:]
		}
	}
	cacheRef := ""
	for len(args) > 0 {
		switch {
		case args[0] == "--":
			args = args[1:]
			goto parsed
		case args[0] == "--cache-ref":
			if len(args) < 2 {
				fmt.Fprintln(stderr, "ctx: --cache-ref needs a registry reference")
				return 2
			}
			cacheRef, args = args[1], args[2:]
		case strings.HasPrefix(args[0], "--cache-ref="):
			cacheRef, args = strings.TrimPrefix(args[0], "--cache-ref="), args[1:]
		default:
			goto parsed
		}
	}

parsed:
	if cacheRef == "" {
		fmt.Fprintln(stderr, "ctx: build requires --cache-ref <registry-ref>")
		return 2
	}
	if len(args) == 0 {
		fmt.Fprintln(stderr, "ctx: build needs build arguments")
		return 2
	}
	operationArgs := append([]string{"--cache-ref", cacheRef, "--"}, args...)
	if named != nil {
		if code := validateEndpoint(resolver, *named, stderr); code != 0 {
			return code
		}
		fmt.Fprintf(stderr, "ctx: building in %s\n", named.description())
		return invokeEndpoint(resolver, *named, "build", operationArgs, os.Stdin, stdout, stderr)
	}
	provider, err := managerProviderForBuild(resolver, providerName)
	if err != nil {
		return reportErrorCode(stderr, err, 2)
	}
	selection, err := adapterSelection(resolver, provider)
	if err != nil {
		return reportError(stderr, err)
	}
	if selection != "" {
		if code := validateEndpoint(resolver, endpoint{Provider: provider, Name: selection}, stderr); code != 0 {
			return code
		}
	} else if _, err := freshMachineInventory(resolver); err != nil {
		return reportError(stderr, err)
	}
	return invokeAdapter(resolver, provider, "build", selection, operationArgs, "", stdout, stderr)
}

func managerProviderForBuild(resolver *config.Resolver, name string) (*modpkg.Adapter, error) {
	if name != "" {
		return managerProvider(name)
	}
	inventory, err := freshMachineInventory(resolver)
	if err != nil {
		return nil, err
	}
	providers := map[string]bool{}
	for _, candidate := range inventory.Contexts {
		if candidate.Runtime == "manager" && candidateOffers(candidate, "build") {
			providers[candidate.Adapter] = true
		}
	}
	if len(providers) == 1 {
		for provider := range providers {
			return managerProvider(provider)
		}
	}
	return managerProvider("")
}

func imageCommand(resolver *config.Resolver, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "ctx: share:manager image requires sync or copy")
		return 2
	}
	switch args[0] {
	case "sync":
		return imageSync(resolver, args[1:], stdout, stderr)
	case "copy":
		return imageCopy(resolver, args[1:], stdout, stderr)
	default:
		fmt.Fprintln(stderr, "ctx: share:manager image requires sync or copy")
		return 2
	}
}

func imageSync(resolver *config.Resolver, args []string, stdout, stderr io.Writer) int {
	tarMode := len(args) > 0 && args[0] == "--tar"
	if tarMode {
		args = args[1:]
	}
	if len(args) < 3 {
		fmt.Fprintln(stderr, "ctx: image sync needs source endpoint, target endpoint, and image names")
		return 2
	}
	source, err := parseEndpointWithDefault(resolver, args[0])
	if err != nil {
		return reportErrorCode(stderr, err, 2)
	}
	target, err := parseEndpointWithDefault(resolver, args[1])
	if err != nil {
		return reportErrorCode(stderr, err, 2)
	}
	images := args[2:]
	if code := validateEndpoint(resolver, source, stderr); code != 0 {
		return code
	}
	if code := validateEndpoint(resolver, target, stderr); code != 0 {
		return code
	}
	if !tarMode && (!endpointSupports(source, "image_push") || !endpointSupports(target, "image_pull")) {
		tarMode = true
		fmt.Fprintln(stderr, "ctx: using archive transfer because registry push/pull is unavailable for an endpoint")
	}
	if tarMode {
		if code := requireEndpointCapability(source, "image_save", stderr); code != 0 {
			return code
		}
		if code := requireEndpointCapability(target, "image_load", stderr); code != 0 {
			return code
		}
	} else {
		if code := requireEndpointCapability(source, "image_push", stderr); code != 0 {
			return code
		}
		if code := requireEndpointCapability(target, "image_pull", stderr); code != 0 {
			return code
		}
	}
	showTransferEndpoints(stderr, source, target)
	if tarMode {
		return copyImagesArchive(resolver, source, target, images, stdout, stderr)
	}
	for _, image := range images {
		if code := invokeEndpoint(resolver, source, "image_push", []string{image}, os.Stdin, stdout, stderr); code != 0 {
			return code
		}
		if code := invokeEndpoint(resolver, target, "image_pull", []string{image}, os.Stdin, stdout, stderr); code != 0 {
			return code
		}
	}
	return 0
}

func imageCopy(resolver *config.Resolver, args []string, stdout, stderr io.Writer) int {
	if len(args) < 3 {
		fmt.Fprintln(stderr, "ctx: image copy needs source endpoint, target endpoint, and image names")
		return 2
	}
	source, err := parseEndpoint(args[0])
	if err != nil {
		return reportErrorCode(stderr, err, 2)
	}
	target, err := parseEndpoint(args[1])
	if err != nil {
		return reportErrorCode(stderr, err, 2)
	}
	if code := validateEndpoint(resolver, source, stderr); code != 0 {
		return code
	}
	if code := validateEndpoint(resolver, target, stderr); code != 0 {
		return code
	}
	if code := requireEndpointCapability(source, "image_save", stderr); code != 0 {
		return code
	}
	if code := requireEndpointCapability(target, "image_load", stderr); code != 0 {
		return code
	}
	showTransferEndpoints(stderr, source, target)
	return copyImagesArchive(resolver, source, target, args[2:], stdout, stderr)
}

func copyImagesArchive(resolver *config.Resolver, source, target endpoint, images []string, stdout, stderr io.Writer) int {
	if err := prepareOutputDirectory("image.archive", os.TempDir(), 1); err != nil {
		return reportError(stderr, err)
	}
	for _, image := range images {
		archive, err := os.CreateTemp("", "ctx-image-*.tar")
		if err != nil {
			return reportError(stderr, err)
		}
		path := archive.Name()
		if err := archive.Close(); err != nil {
			_ = os.Remove(path)
			return reportError(stderr, err)
		}
		code := invokeEndpoint(resolver, source, "image_save", []string{path, image}, os.Stdin, stdout, stderr)
		if code == 0 {
			code = invokeEndpoint(resolver, target, "image_load", []string{path}, os.Stdin, stdout, stderr)
		}
		_ = os.Remove(path)
		if code != 0 {
			return code
		}
	}
	return 0
}

func volumeCommand(resolver *config.Resolver, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "ctx: share:manager volume requires export, import, or copy")
		return 2
	}
	switch args[0] {
	case "export":
		if len(args) != 3 {
			fmt.Fprintln(stderr, "ctx: volume export needs a source endpoint and volume name")
			return 2
		}
		fmt.Fprintln(stderr, "ctx: export is only crash-consistent; stop or quiesce databases first")
		source, err := parseEndpointWithDefault(resolver, args[1])
		if err != nil {
			return reportErrorCode(stderr, err, 2)
		}
		if code := validateEndpoint(resolver, source, stderr); code != 0 {
			return code
		}
		if code := requireEndpointCapability(source, "volume_export", stderr); code != 0 {
			return code
		}
		fmt.Fprintf(stderr, "ctx: exporting from %s\n", source.description())
		return invokeEndpoint(resolver, source, "volume_export", []string{args[2]}, os.Stdin, stdout, stderr)
	case "import":
		if len(args) != 3 {
			fmt.Fprintln(stderr, "ctx: volume import needs a target endpoint and new volume name")
			return 2
		}
		target, err := parseEndpointWithDefault(resolver, args[1])
		if err != nil {
			return reportErrorCode(stderr, err, 2)
		}
		if code := validateEndpoint(resolver, target, stderr); code != 0 {
			return code
		}
		for _, capability := range []string{"volume_exists", "volume_create", "volume_import"} {
			if code := requireEndpointCapability(target, capability, stderr); code != 0 {
				return code
			}
		}
		fmt.Fprintf(stderr, "ctx: importing into %s\n", target.description())
		return importVolume(resolver, target, args[2], os.Stdin, stdout, stderr)
	case "copy":
		if len(args) < 4 || len(args) > 5 {
			fmt.Fprintln(stderr, "ctx: volume copy needs source endpoint, target endpoint, source volume, and optional target volume")
			return 2
		}
		return volumeCopy(resolver, args[1:], stdout, stderr)
	default:
		fmt.Fprintln(stderr, "ctx: share:manager volume requires export, import, or copy")
		return 2
	}
}

func volumeCopy(resolver *config.Resolver, args []string, stdout, stderr io.Writer) int {
	source, err := parseEndpoint(args[0])
	if err != nil {
		return reportErrorCode(stderr, err, 2)
	}
	target, err := parseEndpoint(args[1])
	if err != nil {
		return reportErrorCode(stderr, err, 2)
	}
	sourceVolume := args[2]
	targetVolume := sourceVolume
	if len(args) == 4 {
		targetVolume = args[3]
	}
	if code := validateEndpoint(resolver, source, stderr); code != 0 {
		return code
	}
	if code := validateEndpoint(resolver, target, stderr); code != 0 {
		return code
	}
	if code := requireEndpointCapability(source, "volume_export", stderr); code != 0 {
		return code
	}
	for _, capability := range []string{"volume_exists", "volume_create", "volume_import"} {
		if code := requireEndpointCapability(target, capability, stderr); code != 0 {
			return code
		}
	}
	showTransferEndpoints(stderr, source, target)
	fmt.Fprintln(stderr, "ctx: copy is only crash-consistent; stop or quiesce databases first")
	exists, code := endpointVolumeExists(resolver, target, targetVolume)
	if code != 0 {
		return code
	}
	if exists {
		fmt.Fprintf(stderr, "ctx: target volume %s already exists; refusing to merge data\n", targetVolume)
		return 1
	}
	archive, err := os.CreateTemp("", "ctx-volume-*.tar")
	if err != nil {
		return reportError(stderr, err)
	}
	path := archive.Name()
	defer os.Remove(path)
	if code := invokeEndpoint(resolver, source, "volume_export", []string{sourceVolume}, os.Stdin, archive, stderr); code != 0 {
		_ = archive.Close()
		return code
	}
	if err := archive.Close(); err != nil {
		return reportError(stderr, err)
	}
	input, err := os.Open(path)
	if err != nil {
		return reportError(stderr, err)
	}
	defer input.Close()
	return createAndImportVolume(resolver, target, targetVolume, input, stdout, stderr)
}

func importVolume(resolver *config.Resolver, target endpoint, volume string, input io.Reader, stdout, stderr io.Writer) int {
	exists, code := endpointVolumeExists(resolver, target, volume)
	if code != 0 {
		return code
	}
	if exists {
		fmt.Fprintf(stderr, "ctx: target volume %s already exists; refusing to merge data\n", volume)
		return 1
	}
	return createAndImportVolume(resolver, target, volume, input, stdout, stderr)
}

func createAndImportVolume(resolver *config.Resolver, target endpoint, volume string, input io.Reader, stdout, stderr io.Writer) int {
	if code := invokeEndpoint(resolver, target, "volume_create", []string{volume}, os.Stdin, stdout, stderr); code != 0 {
		return code
	}
	return invokeEndpoint(resolver, target, "volume_import", []string{volume}, input, stdout, stderr)
}

func parseEndpoint(value string) (endpoint, error) {
	if strings.HasPrefix(value, "@") {
		registry, err := readManagerRegistry()
		if err != nil {
			return endpoint{}, err
		}
		instance, ok := findManagerInstance(registry, strings.TrimPrefix(value, "@"))
		if !ok {
			return endpoint{}, fmt.Errorf("unknown manager instance %s; run ctx manager ls", value)
		}
		provider, err := managerProvider(instance.Provider)
		if err != nil {
			return endpoint{}, err
		}
		return endpoint{Provider: provider, Name: instance.Selection, Instance: &instance}, nil
	}
	providerName, selection, ok := strings.Cut(value, ":")
	if !ok || providerName == "" || selection == "" {
		return endpoint{}, fmt.Errorf("endpoint must be @<instance> or <manager-provider>:<context>")
	}
	provider, err := managerProvider(providerName)
	if err != nil {
		return endpoint{}, err
	}
	return endpoint{Provider: provider, Name: selection}, nil
}

func parseEndpointWithDefault(resolver *config.Resolver, value string) (endpoint, error) {
	if strings.Contains(value, ":") || strings.HasPrefix(value, "@") {
		return parseEndpoint(value)
	}
	inventory, err := freshMachineInventory(resolver)
	if err != nil {
		return endpoint{}, err
	}
	providers := map[string]bool{}
	for _, candidate := range inventory.Contexts {
		if candidate.Runtime != "manager" {
			continue
		}
		if _, err := inventory.Find(candidate.Adapter, value); err == nil {
			providers[candidate.Adapter] = true
		}
	}
	if len(providers) == 1 {
		for name := range providers {
			provider, err := managerProvider(name)
			if err != nil {
				return endpoint{}, err
			}
			return endpoint{Provider: provider, Name: value}, nil
		}
	}
	provider, err := managerProvider("")
	if err != nil {
		if len(providers) > 1 {
			return endpoint{}, fmt.Errorf("context %s is offered by multiple manager adapters; use <provider>:<context>", value)
		}
		return endpoint{}, err
	}
	if len(providers) > 1 && !providers[provider.Manifest.Name] {
		return endpoint{}, fmt.Errorf("context %s is offered by multiple manager adapters; use <provider>:<context>", value)
	}
	return endpoint{Provider: provider, Name: value}, nil
}

func managerProvider(name string) (*modpkg.Adapter, error) {
	if name == "" {
		store := adapterStore()
		installed, err := store.List()
		if err != nil {
			return nil, err
		}
		var defaultProvider *modpkg.Adapter
		for _, candidate := range installed {
			if !candidate.IsRuntime("manager") || !candidate.Manifest.DefaultProvider {
				continue
			}
			trusted, trustErr := store.IsTrusted(candidate)
			if trustErr != nil || !trusted {
				continue
			}
			if defaultProvider != nil {
				return nil, fmt.Errorf("multiple default manager providers: %s and %s", defaultProvider.Manifest.Name, candidate.Manifest.Name)
			}
			defaultProvider = candidate
		}
		if defaultProvider == nil {
			return nil, fmt.Errorf("no trusted default manager provider is installed; use <provider>:<context>")
		}
		return defaultProvider, nil
	}
	provider, err := adapterStore().Load(name)
	if err != nil {
		return nil, fmt.Errorf("unknown manager provider %s: %w", name, err)
	}
	if !provider.IsRuntime("manager") {
		return nil, fmt.Errorf("adapter %s is not a manager provider", name)
	}
	return provider, nil
}

func validateEndpoint(resolver *config.Resolver, value endpoint, stderr io.Writer) int {
	if _, err := freshMachineInventory(resolver); err != nil {
		return reportError(stderr, err)
	}
	if code := requireEndpointCapability(value, "validate", stderr); code != 0 {
		return code
	}
	return invokeAdapterIOWithEnv(resolver, value.Provider, "validate", value.Name, nil, "", os.Stdin, io.Discard, stderr, value.environment())
}

func invokeEndpoint(resolver *config.Resolver, value endpoint, operation string, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if code := requireEndpointCapability(value, operation, stderr); code != 0 {
		return code
	}
	return invokeAdapterIOWithEnv(resolver, value.Provider, operation, value.Name, args, "", stdin, stdout, stderr, value.environment())
}

func requireEndpointCapability(value endpoint, operation string, stderr io.Writer) int {
	if endpointSupports(value, operation) {
		return 0
	}
	fmt.Fprintf(stderr, "ctx: manager provider %s does not support %s\n", value.Provider.Manifest.Name, operation)
	return 2
}

func endpointSupports(value endpoint, operation string) bool {
	if !value.Provider.HasCapability(operation) {
		return false
	}
	system, err := systemgraph.Open(configHomePath())
	if err != nil {
		return true
	}
	defer system.Close()
	inventory, err := system.ResolveInventory(context.Background(), "manager")
	if err != nil || !inventory.FreshWithin(5*time.Second) {
		return true
	}
	if _, err := inventory.Find(value.Provider.Manifest.Name, value.Name); err != nil {
		// A declared endpoint can be absent from list (for example a pinned
		// remote address); its live adapter validation is decisive.
		return true
	}
	_, err = inventory.Find(value.Provider.Manifest.Name, value.Name, operation)
	return err == nil
}

func endpointVolumeExists(resolver *config.Resolver, value endpoint, volume string) (bool, int) {
	if !value.Provider.HasCapability("volume_exists") {
		return false, 2
	}
	code := invokeAdapterIOWithEnv(resolver, value.Provider, "volume_exists", value.Name, []string{volume}, "", os.Stdin, io.Discard, io.Discard, value.environment())
	if code == 0 {
		return true, 0
	}
	if code == 1 {
		return false, 0
	}
	return false, code
}

func (value endpoint) environment() map[string]string {
	environment := map[string]string{}
	for _, key := range value.Provider.Manifest.OverrideEnv {
		environment[key] = ""
	}
	if value.Instance != nil && value.Instance.Address != "" {
		environment["CTX_MANAGER_ADDRESS"] = value.Instance.Address
	}
	if value.Instance != nil {
		for key, val := range managerInstanceEnvironment(*value.Instance) {
			environment[key] = val
		}
	}
	return environment
}

func (value endpoint) description() string {
	selection := value.Provider.Manifest.Name + ":" + value.Name
	if value.Instance == nil {
		return selection
	}
	return fmt.Sprintf("@%s (%s via %s)", value.Instance.Name, managerDescription(*value.Instance), selection)
}

func showTransferEndpoints(stderr io.Writer, source, target endpoint) {
	fmt.Fprintf(stderr, "ctx: sharing %s -> %s\n", source.description(), target.description())
}
