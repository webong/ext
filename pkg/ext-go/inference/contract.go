// Package inference bridges the portable res/int/inference contract with the Ext
// plugin engine. It declares the inference.provider/v1 plugin contract and
// supplies host and guest helpers so any res/int/inference Provider can be served
// as an Ext plugin guest and invoked by a host through a plugin session.
//
// The package depends only on res/int/inference (the contract) and
// pkg/ext-go/engine (the plugin mechanics). It knows nothing about CTX, CTN,
// Cymonkey, or any concrete provider.
package inference

import (
	"context"
	plugin "github.com/webong/ext/pkg/ext-go/engine"
	"github.com/webong/ext/pkg/ext-go/engine/guest/stream"
	"github.com/webong/ext/res/int/inference"
)

// ContractName is the recorded plugin contract name (see docs/contract-names.md).
const ContractName = "inference.provider"

// ContractVersion is the contract version.
const ContractVersion = "v1"

// ContractRef is the exact contract reference for inference.provider/v1.
var ContractRef = plugin.ContractRef{Name: ContractName, Version: ContractVersion}

// Host and guest construction types are re-exported here so inference programs
// depend on the integration module instead of importing the engine directly.
type Identity = plugin.Identity
type DescriptorType = plugin.Descriptor
type Session = plugin.Session
type Options = plugin.Options
type Request = plugin.Request
type Backend = plugin.Backend

func Open(ctx context.Context, d plugin.Descriptor, opts plugin.Options) (*plugin.Session, error) {
	return plugin.Open(ctx, d, opts)
}

// Operation names for the inference.provider/v1 contract. Surfaces are
// admission classifications with no built-in authority.
const (
	OpCapabilities = "capabilities" // observation
	OpHealth       = "health"       // observation
	OpGenerate     = "generate"     // action
	OpStream       = "stream"       // action (opens an ext.stream of GenerationEvent)
	OpEmbed        = "embed"        // action
	OpTranscribe   = "transcribe"   // action
	OpSynthesize   = "synthesize"   // action (opens an ext.stream of audio bytes)
	OpModels       = "models"       // observation
	OpClose        = "close"        // action
)

// Operations returns the full declared operation set for the contract, with
// surfaces. Stream and synthesize declare progress because they may emit
// stream frames before completion.
func Operations() []plugin.Operation {
	return []plugin.Operation{
		{Name: OpCapabilities, Surface: "observation"},
		{Name: OpHealth, Surface: "observation"},
		{Name: OpGenerate, Surface: "action"},
		{Name: OpStream, Surface: "action", Progress: true},
		{Name: OpEmbed, Surface: "action"},
		{Name: OpTranscribe, Surface: "action"},
		{Name: OpSynthesize, Surface: "action", Progress: true},
		{Name: OpModels, Surface: "observation"},
		{Name: OpClose, Surface: "action"},
	}
}

// Descriptor builds the plugin Descriptor for an inference provider plugin
// given its identity. The provider advertises exactly the operations it
// supports through res/int/inference.ProviderInfo.Capabilities; the descriptor
// declares the complete contract surface and the guest enforces support at
// call time.
func Descriptor(id plugin.Identity) plugin.Descriptor {
	return plugin.Descriptor{
		APIVersion: plugin.APIVersion,
		Identity:   id,
		Contracts: []plugin.Contract{
			{ContractRef: ContractRef, Operations: Operations()},
			{ContractRef: stream.Contract(), Operations: []plugin.Operation{{Name: "open"}, {Name: "read"}, {Name: "close"}}},
		},
	}
}

// CapabilityOperation maps a res/int/inference capability to its contract
// operation. A provider that does not advertise a capability must return
// ErrUnsupported for that operation.
func CapabilityOperation(c inference.Capability) (string, bool) {
	switch c {
	case inference.CapabilityGenerate:
		return OpGenerate, true
	case inference.CapabilityStream:
		return OpStream, true
	case inference.CapabilityEmbed:
		return OpEmbed, true
	case inference.CapabilityTranscribe:
		return OpTranscribe, true
	case inference.CapabilitySynthesize:
		return OpSynthesize, true
	case inference.CapabilityModelListing:
		return OpModels, true
	default:
		return "", false
	}
}
