# clipboard v1

`cases.json` covers the pure rules of the clipboard contract: a policy (`maxBytes`, allowed
`formats`) and a list of items checked against a byte ceiling and the allowed formats.

- **policy**: the byte ceiling is 1 to 16 MiB, there are 1 to 32 formats, and each format name is
  not empty, at most 256 UTF-8 bytes (not characters or UTF-16 units), and unique (names are
  case-sensitive). Anything else is `invalid`.
- **items**: the checks run in this order, and the first failure decides. The byte ceiling must be
  1 to 16 MiB (`invalid`); at most 64 items (`too_large`); then each item in order: 1 to 32
  representations (`invalid`); then each representation in order: its format is allowed and not
  already in the item (`denied`), and the running total of bytes, across the items so far, does not
  exceed the ceiling (`too_large`). No items at all is valid (it is how the clipboard is cleared).

Not covered, because a binding cannot reproduce it without the native clipboard: the generation
check of a write and its `changed` code, the Windows one-item rule, and reading. The Go-only DIB
header checks of `capture/clipboard` stay in Go.
