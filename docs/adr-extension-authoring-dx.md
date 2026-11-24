# ADR: Extension-author experience for application-owned APIs

Status: accepted direction, 2026-11-24. The authoring workflow below is a target,
not a claim that its tooling is implemented. Exact APIs and CLI syntax remain to
be designed against consumers.

## Context

Ext helps software authors make their applications extensible. Kvlite, fslite
and gateway are existing ecosystem consumers identified by the user; CTX, CTN
and CTO are also consumers. Ext's purpose extends beyond its own products.
This ADR does not assert which extension points those external repositories
currently implement; inspecting their actual integration is the first step.

Kubernetes offers useful developer-experience ideas: discoverable extension
points, declarative registration, versioned schemas, consistent inspection,
conformance tooling and controller scaffolding. Its extension mechanisms have
different lifecycles; a common transport alone does not provide that experience.
See [Kubernetes extension points](https://kubernetes.io/docs/concepts/extend-kubernetes/).

Ext already separates domain contracts from runtime bindings. The missing
decision is how to assemble those primitives into a consistent experience for
an application exposing APIs and an independent author implementing them.

## Existing decisions and implementation

This ADR complements, rather than replaces, these foundations:

| Foundation | Existing source | Reuse here |
| --- | --- | --- |
| Descriptors, exact contract versions, admission, sessions and package manifests | [Plugin contract](adr-plugin-contract.md) | The vocabulary and invocation rules |
| Typed methods, validation and descriptor generation | `pkg/ext-go/engine/plugin/guest/author` | Authoring primitives; not yet a complete scaffold generator |
| Runtime and language interoperability | [Hosts in every language](adr-hosts-in-every-language.md), [runtime guide](plugin-runtimes.md) | Supported host/guest bindings and their conformance checks |
| Sealed adapter packages | [Adapter package ADR](adr-adapter-package.md) | Adapters use the existing package format |
| Archive install and receipts | [Engine parity evidence](plugin-engine-parity.md#evidence-for-the-current-increment), `engine/plugin/host/store` in the Go engine module | Pinned archives, bounded extraction, artifact verification and install receipts |
| Fetching native libraries for Go builds | [Library opener ADR](adr-library-opener.md) | Native build dependencies, a distinct workflow from installing an extension |

The archive work is the distribution stage of the authoring experience. It
does not describe the application's extension points, generate implementations
or prove behavioral compatibility. We will reuse it, not introduce another
archive format, package manifest or trust system. A verified digest establishes
integrity; installation neither grants execution authority nor proves readiness.

Native library authoring already goes further: `lib/plugin/interface.json` and
`lib/ledger/interface.json` describe operations, schemas and events; the libraries
return those descriptions through `describe`. `pkg/ext-go/cengine/bind` generates
typed Go bindings, exposed through `ext bundle bind`, while `ext bundle fetch` obtains
verified native artifacts from citations. Reuse this experience rather than
designing generation from scratch. These describe the C library interface,
not the application-owned plugin contracts; a host extension catalog and guest
scaffolding still need their own integration with existing descriptors.

Decision 8 of the [library opener ADR](adr-library-opener.md) makes C the host
implementation for Go as well as other languages. New host tooling must target
that interface. The pure-Go guest SDK and shared data model remain useful;
the retiring pure-Go host and ledger are not foundations for new features.

## Decision

Provide reusable kits for applications to expose their own extension points,
with a consistent author workflow:

1. Discover the host's extension points and inspect their contracts.
2. Scaffold an implementation for a supported language and runtime.
3. Implement typed operations and any explicitly granted host callbacks.
4. Run the application's contract tests and Ext's protocol conformance checks.
5. Build, seal, package and install using existing package mechanisms.
6. Inspect compatibility, permission and readiness; diagnose failures.

Embedding these libraries does not require adopting an Ext product or running
the `ext` executable. Generic authoring commands may be exposed through `ext`,
the only public executable shipped by this repository. Consumers may integrate
the same library operations into their own tools. This ADR specifies no new CLI
commands and does not change product dispatch.

### Discoverable, application-owned contracts

A host should be able to describe the extension points it accepts: contract
names and exact versions, operations, input/output schemas where available,
required capabilities and lifecycle expectations. Installed guest descriptors
describe implementations; the host's catalog describes what can be extended.
These are related views, not interchangeable claims.

Reuse descriptors and `ext.schema/v1` wherever they express the requirement.
Identify missing semantics before adding a format or protocol operation.
Discovery must be read-only and must not require executing untrusted packages.
Remote discovery, when a consumer exposes it, uses that consumer's authenticated
API and disclosure policy; this decision adds no remote server to Ext.

### Contract-driven authoring and tests

Generate or scaffold typed clients, guest handlers, manifest templates and
reference documentation from an explicit contract source. Preserve exact
version selection; do not infer compatibility from matching method names or
silently introduce version ranges. Generated output must be deterministic and
must never overwrite user implementations without an explicit request.

Keep two test responsibilities visible: Ext proves protocol/backend behavior;
the application supplies fixtures and tests for its own API semantics. A guest
that passes the former is not automatically a correct implementation of the
latter. Start with languages and runtimes used by actual consumers.

### Consistent inspection

Report separately whether an extension is discovered, structurally valid,
compatible, verified, permitted and ready. A package can pass one stage and
fail another. Diagnostics should identify the failed stage, stable error code
and a useful remedy without exposing secrets or payloads. Reuse existing
session states and errors; do not invent a second lifecycle beside them.

### Lifecycle-specific helpers

Callable extensions, persistent services and resource controllers can share
contracts and tooling, but need different lifecycle helpers. A transport such
as JSON lines or gRPC does not determine those semantics or make an unrelated
native interface compatible.

Controller helpers are deferred until a consumer needs desired-state APIs.
Before implementing them, specify resource identity/versioning, watch resume
and resynchronization, idempotency, retry/backoff, deletion, status and ownership.
The ledger may supply persistence and observation; it is not by itself a
controller framework. Generic session calls remain non-retrying: reconciliation
retries require the application's explicit semantics.

## Ownership and limits

Applications own their API vocabulary, resource schemas, hook ordering,
selection, authentication, grants, failure policy and approval. Ext owns generic
contracts, validation, invocation, package mechanics and reusable tooling.
Product/provider-specific integrations stay in their adapters. No shared
library switches on `kvlite`, `fslite`, `gateway` or another consumer's name.

This decision does not build a Kubernetes API server, scheduler, cluster
orchestrator, universal REST gateway or CRD clone. It does not make existing
application APIs extensible automatically. Hosts must deliberately expose and
implement their extension points. Existing protocols require explicit bindings.

Changes to the plugin contract or manifest follow the C-first rule, shared
fixtures and Go parity requirements. Convenience tooling alone does not require
a new wire version. Reusable code stays in public library modules and does not
depend on product-private code.

## Delivery and acceptance

1. Inspect kvlite, fslite and gateway's current integrations. Record existing
   contracts, author steps, duplicated tooling and concrete gaps. Select one
   real extension point with its consumer's owner; do not invent a replacement
   API for that application.
2. Make that contract discoverable and provide a repeatable scaffold, typed
   implementation path and application-owned conformance suite. Reuse existing
   authoring and packaging code first.
3. Exercise the full workflow outside the Ext workspace with pinned available
   dependencies, no local replacement directives and no implicit development
   artifacts. An independent author must be able to build, test, package,
   install and invoke the extension from the documented steps.
4. Verify incompatible versions, invalid payloads, tampered archives, denied
   capabilities, cancellation and extension failure produce actionable results.
   Where supported by the selected contract, prove two implementations in
   different languages work without changing host business logic.
5. Apply the workflow to a second real application before generalizing the
   tooling. Add persistent-service or controller helpers only for demonstrated
   consumer requirements, with separate lifecycle acceptance tests.

Publishing versions is a separate operation. Tests against an exported source
snapshot may aid development but must not be described as published-dependency
acceptance. This ADR authorizes no changes in the consumer repositories.

## Consequences

Ext is evaluated by the experience of an application author and an extension
author, as well as by engine conformance. Existing runtime and archive work
becomes one connected workflow. The cost is maintaining contract tooling and
diagnostics across supported languages; grounding each increment in real
consumers limits speculative abstractions and duplicated infrastructure.
