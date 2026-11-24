// Package model preserves the CTN import path for the Ext tensor model bridge.
//
// Deprecated: use github.com/webong/ext/res/int/model/plugin.
package model

import (
	"context"

	plugin "github.com/webong/ext/pkg/ext-go/engine/plugin"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/guest/author"
	packagekit "github.com/webong/ext/pkg/ext-go/engine/plugin/package"
	domain "github.com/webong/ext/res/int/model"
	modelplugin "github.com/webong/ext/res/int/model/plugin"
)

const (
	ContractName       = modelplugin.ContractName
	ContractVersion    = modelplugin.ContractVersion
	Protocol           = modelplugin.Protocol
	MaxWireTensorBytes = modelplugin.MaxWireTensorBytes
)

var ContractRef = modelplugin.ContractRef

type InferRequest = modelplugin.InferRequest
type InferResponse = modelplugin.InferResponse
type Client = modelplugin.Client
type ExecutablePackage = modelplugin.ExecutablePackage

func NewClient(caller author.Caller) *Client { return modelplugin.NewClient(caller) }

func RegisterProvider(registry *author.Registry, provider domain.Provider, authorize func(context.Context, plugin.Request) error) (*plugin.Descriptor, error) {
	return modelplugin.RegisterProvider(registry, provider, authorize)
}

func ServeCurrentExecutable(ctx context.Context, id, version string, provider domain.Provider, authorize func(context.Context, plugin.Request) error) error {
	return modelplugin.ServeCurrentExecutable(ctx, id, version, provider, authorize)
}

func ServeStdio(ctx context.Context, id plugin.Identity, provider domain.Provider, authorize func(context.Context, plugin.Request) error) error {
	return modelplugin.ServeStdio(ctx, id, provider, authorize)
}

func PackageExecutable(options ExecutablePackage) (packagekit.Manifest, error) {
	return modelplugin.PackageExecutable(options)
}
