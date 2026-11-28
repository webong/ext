# Browser policy wire fixtures

`v2/cases.json` is independent of the language used by a browser adapter. An
adapter receiving `share PROFILE -- policy export` reads the JSON request from
standard input and writes one JSON policy bundle to standard output. The host
sets `source` after validating the bundle. Empty `entries` means no policy
sources were found; missing `entries` is an invalid response.

Each implementation should run the request and bundle cases against its own
decoder and encoder. The `src/ctx/web/contract` test runs the Go cases; other
adapter languages can consume the same file without importing Go.
