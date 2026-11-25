// Shared contracts are defined in core; aliases preserve type identity.
package ledger

import shared "github.com/webong/ext/pkg/ext-go/core/ledger/observation"

const Namespace = shared.Namespace

type AdapterObservation = shared.AdapterObservation
type InstallerObservation = shared.InstallerObservation
type ContextObservation = shared.ContextObservation
type ResourceObservation = shared.ResourceObservation
type RelationObservation = shared.RelationObservation
type AliasObservation = shared.AliasObservation
type ContextCandidate = shared.ContextCandidate
type ContextProgram = shared.ContextProgram
type Inventory = shared.Inventory
type HostInventory = shared.HostInventory
type OperationRequirements = shared.OperationRequirements
type ShellRequirement = shared.ShellRequirement
type FilesystemRequirement = shared.FilesystemRequirement
type WebviewRequirement = shared.WebviewRequirement
type HostSnapshot = shared.HostSnapshot
type HostCandidates = shared.HostCandidates
type PreparedHost = shared.PreparedHost
type WebviewValidator = shared.WebviewValidator
type HostRequirementError = shared.HostRequirementError

var SafeOrigin = shared.SafeOrigin
