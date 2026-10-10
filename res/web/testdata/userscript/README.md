# ext.userscript/v1alpha1 fixtures

Language-neutral test vectors for the userscript rules in `res/web/userscript`.
The Go tests (`userscript/fixtures_test.go`) must pass them; any other
implementation, such as a TypeScript parser, can run the same JSON files.
A fixture with an `error` expects rejection with that stable code; messages are
not part of the contract. `userscript.ErrorCode(err)` returns the code.

Scope of v1alpha1: scripts run at document start in the page world with
`@grant none`, and every match is an explicit pattern. `<all_urls>` is rejected
on purpose, since a blanket match is the unsafe default. Other `@run-at`
values, execution worlds and grants are rejected with stable codes. Widening
the scope is an additive `v1alpha2`.

| File | Covers |
| --- | --- |
| `v1alpha1/match-patterns.json` | Match and exclude patterns: schemes, hosts, `file://` host rule, length bounds |
| `v1alpha1/directives.json` | Unsupported header directives, `@run-at`, `@inject-into`, `@grant`, header/record match agreement |
| `v1alpha1/metadata.json` | Name, match and exclude inference from a header, and id derivation |
| `v1alpha1/record.json` | Whole-record validation: target, id, name, source size, revision, match counts |
| `v1alpha1/revision.json` | Revision digest vectors |

Error codes: `invalid_target`, `invalid_id`, `invalid_name`, `invalid_source_size`,
`revision_mismatch`, `invalid_matches`, `invalid_match_pattern`,
`invalid_file_host`, `unsupported_grant`, `unsupported_require`,
`unsupported_directive`, `unsupported_run_at`, `unsupported_world`,
`match_metadata_mismatch`.

## Revision

`revision = "sha256:" + hex(sha256(JSON))`, where JSON is the compact UTF-8
object `{"target","id","name","matches","excludeMatches","source"}` in that key
order. `<`, `>`, `&`, U+2028 and U+2029 are written as `<`, `>`,
`&`, ` `, ` `, as Go's `encoding/json` does by default. An absent
`excludeMatches` is JSON `null`, not `[]`, and the two have different digests,
so an implementation must keep the distinction. In `record.json` the revision
`"auto"` means the digest of the other fields.

## Using the fixtures

Copy the `v1alpha1` directory into the consumer's test tree, or fetch it from a
pinned ext tag, and drive the consumer's parser and validator from each file:
accept the cases without `error`, and reject the others. Fixtures cover the
shared rules above; the object shapes of a consumer's manifest need not match
the Go `Record`.
