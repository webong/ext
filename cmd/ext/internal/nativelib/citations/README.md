# Pinned library releases

`ext` embeds every `<library>.citation.json` in this directory (`ext_plugin.citation.json`,
`ext_ledger.citation.json`). Each one is the citation an `ext-plugin` or `ext-ledger` release
publishes: the URL and SHA-256 of each platform's archive. When `ext` finds no copy of a library
locally, it downloads the archive the citation pins for its platform, checks the hash and keeps the
library in the user cache (see `../nativelib.go`).

Copy the citations from the releases whose `interface.json` matches the bindings this `ext` is built
with. Do not edit them by hand. With no citation here, `ext` does not download and reports where it
looked instead.
