# Shared resources

`res` holds portable resource workflows used by both CTX and its adapters.
`res/browser` provides browser cookie, extension, userscript, and bookmarklet
helpers. Browser-specific discovery, storage, signing, installation, and
activation remain in the owning packages under `adapters/`.
Its `contract` package shares versioned browser types and validation between
hosts and guests; `guest` contains portable request-serving helpers.

`res/credential` provides the portable credential adapter protocol and a
client for requesting declared credential dependencies through CTX. Native
store access remains in the keychain, Secret Service, and Credential Manager
adapters.
