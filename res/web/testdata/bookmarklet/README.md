# ext.bookmarklet/v1alpha1 fixtures

Language-neutral vectors for `res/web/bookmarklet`. The Go tests
(`bookmarklet/fixtures_test.go`) must pass them, and another implementation can
run the same files.

| File | Covers |
| --- | --- |
| `v1alpha1/encode.json` | `Encode`: exact output URLs, plus sources that must be rejected |
| `v1alpha1/decode.json` | `Decode`: exact sources, plus URLs that must be rejected |
| `v1alpha1/install-page.json` | `InstallPage`: escaping, checked by substring, plus rejected input |

The encoded URL expected values were produced by an independent percent-encoder,
not by the Go code. Each file's `doc` states the rule. Decoding never evaluates
the script.
