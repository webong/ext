# Cookie contract fixtures (wire version 2)

Language-neutral vectors for the portable cookie rules in `res/web/contract`.
The Go tests (`contract/cookie_fixtures_test.go`) must pass them, and another
implementation can run the same files. Cases give `valid` or `match` outcomes;
error text is not part of the contract, except that it never contains the
cookie value.

| File | Covers |
| --- | --- |
| `v2/site.json` | `ParseSite`: http and https only, hostname required, no userinfo, host lower-cased |
| `v2/domain-match.json` | `CookieDomainMatches` |
| `v2/path-match.json` | `CookiePathMatches`: prefix match only at a path boundary |
| `v2/cookie.json` | `ValidateCookie`: names, domains, paths, secure prefixes, SameSite, expiry range, partition scope, adapter attributes |
| `v2/bundle.json` | `ValidateCookieBundle`: version, site scope, expiry, SameSite policy |

Cookie JSON uses the contract's field names (`http_only`, `same_site_policy`,
`has_cross_site_ancestor`, `partition_key`). An `expiry` of 0 is a session
cookie and 1 is already expired.
