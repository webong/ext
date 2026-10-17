# Adapters are ext packages

An adapter used to have its own manifest, `adapter.toml`: flat `key = "value"`
lines with no schema, parsed by hand, beside a package manifest, `plugin.json`
(`ext.package/v1`), that every other ext package has. Two manifests meant two
parsers, two trust anchors, two archive formats and a file name (`adapter.toml`,
later `plugin.toml` in a draft of this change) that suggested a different kind of
thing. An adapter is now an ext package whose manifest is `plugin.json`. This page
records the decisions; [the adapter API](adapter-api.md) has the reference.

## Decisions

**One manifest, no fallback.** The loader reads `plugin.json` and nothing else.
There is no `adapter.toml` reader and no migration path: ext is in development, so
an adapter installed before this change fails to load (its error says that
`adapter.toml` is no longer read) and has to be reinstalled and trusted again.

**The adapter API stays 2.0.** The invocation protocol (argv and streams per
operation) and the capabilities did not change; only where they are declared did.
The version is now the version of the `ext.adapter` contract. Bumping it would have
forced every `dependencies_*` declaration and every host that compares versions to
change for no behavioural reason.

**The fields.** The name is the package identity. The capabilities are the
operations of the `ext.adapter` contract; `browser_share` and `browser_management`
are operations of `ext.adapter.browser.share` and `ext.adapter.browser.management`,
so a caller asks the descriptor which operations an adapter has. The launchers are
artifacts and `argv` entrypoints named `main` and `main.windows`. Everything ctx
interprets (runtime, surfaces, selection keys, share spaces, dependencies per
operating system, hook setup, browser query preferences, `go_entry`) is one
metadata object, `ctx.adapter/v1`, whose schema belongs to ctx and is closed.
ext.package/v1 did not need to grow: per-OS dependencies, the one thing it cannot
express, live in the metadata.

**Template and seal.** An adapter's executable is built, so its digest does not
exist in the repository. The checked-in `plugin.json` is a template with empty
digests and revision. Sealing (`adapter.Seal`, run by install, build and pack)
fills them in, records the platform on the launchers, and lists every other file in
the directory as an artifact, so first-party helper binaries built beside a
launcher keep shipping. A sealed manifest lists exactly the files present, which
is the rule the package store applies, and install, build and pack always seal. A
directory copied into the home by hand keeps its template and still loads; the trust
digest of its files covers it like any other adapter. For a platform, the launcher of
the other kind is dropped: an installed Windows adapter has `main.windows`, any
other has `main`.

**Trust is unchanged.** The trust record is still the digest of the whole adapter
directory (which now includes the sealed `plugin.json`), keyed by name, and an
adapter is still installed untrusted. Every adapter therefore shows as changed
once, when it is reinstalled in the new form.

**One archive format.** A `.ctxadapter` archive is the sealed package directory in
a zip. The second record, `ctx-package.json`, is gone: the name and platform are
in the manifest. The platform index is unchanged.

**The loader.** `contract/host/adapter` stays a thin layer: `Adapter.Manifest` is
the projection hosts already use, filled from the package document, and
`Adapter.Package` is the document itself. The exported API hosts depend on
(`NewStore`, `Home`, `Manifest`, `HasCapability`, `HasBrowserShare`,
`HasBrowserManagement`, `CommandContext`, `Checksum`, `IsTrusted`, `TrustState`)
did not change. `LoadSource` validates a source template without requiring its
executable to exist yet.

## Why not the other options

Keeping `adapter.toml` and adding `plugin.toml` would have made two schemas with
one stem. Reading both for a release would have kept installed adapters working,
at the cost of a second parser and a second trust anchor, for a project with no
installed users. Putting the ctx fields into new ext.package/v1 fields would have
made ext interpret product vocabulary, which the ownership rules forbid.

## Checks

The 42 manifests in the repository (40 adapters and 2 examples) were converted by
a program, each checked by reading the new document back and comparing every field
with the old one. An arch test loads every adapter source with the real loader on
all three platforms and fails if an adapter has no `plugin.json` or still has an
old manifest. Tests cover the mapping of every field, sealing for each platform,
install, pack, build, archives (another platform, a changed file, an unlisted file)
and the platform index.
