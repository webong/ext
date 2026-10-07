// Package adapter is the host side of the adapter system: manifests, the
// installed-adapter store, package lifecycle, trust checks and executable
// invocation. Any ext product can use it; a product supplies only the store
// home and the wording of its trust hint. The adapter process protocol that
// executables implement lives in pkg/plugin (the root package).
package adapter
