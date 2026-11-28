// Package resource transfers opaque browser resources between caller-selected
// backends. Adapters own the payload and hosts own selection and authorization.
package resource

import (
	"context"
	"errors"

	"github.com/webong/ext/res/web/host/contract"
)

// Invoke calls one operation on an already selected and authorized backend.
// A caller may use a different implementation for source and destination.
type Invoke func(context.Context, string, string, contract.ResourceRequest, any) error

// Export obtains a resource from source, validates its portable envelope, and
// stamps the caller's source label without inspecting the adapter-owned payload.
func Export(ctx context.Context, invoke Invoke, resource, source string, args []string) (contract.ResourceBundle, error) {
	if ctx == nil || invoke == nil || resource == "" || source == "" {
		return contract.ResourceBundle{}, errors.New("browser resource export needs a context, backend, resource, and source")
	}
	if err := ctx.Err(); err != nil {
		return contract.ResourceBundle{}, err
	}
	var bundle contract.ResourceBundle
	request := contract.ResourceRequest{Version: contract.Version, Args: args}
	if err := invoke(ctx, resource, "export", request, &bundle); err != nil {
		return contract.ResourceBundle{}, err
	}
	if err := contract.ValidateResourceBundle(bundle, resource); err != nil {
		return contract.ResourceBundle{}, err
	}
	bundle.Source = source
	return bundle, nil
}

// Import validates the envelope before giving its opaque payload to the
// destination. The destination adapter decides whether it can preserve it.
func Import(ctx context.Context, invoke Invoke, resource string, args []string, bundle contract.ResourceBundle, replace bool) error {
	if ctx == nil || invoke == nil {
		return errors.New("browser resource import needs a context and backend")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := contract.ValidateResourceBundle(bundle, resource); err != nil {
		return err
	}
	request := contract.ResourceRequest{Version: contract.Version, Args: args, Bundle: &bundle, Replace: replace}
	return invoke(ctx, resource, "import", request, nil)
}

// Copy exports from one authorized backend and imports into another.
func Copy(ctx context.Context, source, destination Invoke, resource, sourceLabel string, args []string, replace bool) (contract.ResourceBundle, error) {
	bundle, err := Export(ctx, source, resource, sourceLabel, args)
	if err != nil {
		return contract.ResourceBundle{}, err
	}
	if err := Import(ctx, destination, resource, args, bundle, replace); err != nil {
		return contract.ResourceBundle{}, err
	}
	return bundle, nil
}
