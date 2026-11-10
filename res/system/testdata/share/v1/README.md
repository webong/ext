# share v1

`cases.json` covers the share contract: the limits a caller fixes, and the normalisation of share
items into one bounded payload.

- **Limits**: `maxItems` 1 to 128, `maxBytes` 1 to 64 MiB, `maxTextBytes` 1 to 1 MiB and
  `maxUrlLength` 1 to 8192, else `invalid`. The defaults are in the file. Go reads a zero limit as the
  default and cannot tell it from an absent one, so an explicit zero is not a case; the other
  bindings take their defaults from their default constructor.
- **Trim**: strip leading and trailing U+0009 to U+000D and U+0020. Unicode spaces and the other
  control characters are content.
- **Order**: more items than `maxItems` is `too_large` before anything else. Then each item in
  order: subject and text (a NUL is `invalid`; more than `maxTextBytes` UTF-8 bytes is `too_large`),
  then its links (more distinct links than `maxItems`, counted before any link is judged, is
  `too_large`), then its attachments. The merged subject and text are checked last. The first
  failure decides.
- **Text**: empty text is ignored, exact duplicates collapse, and the rest is joined with a line
  feed. Text is never trimmed.
- **Links**: the normal form is the trimmed link with its scheme lowercased and nothing else
  changed, so two links are the same link when their normal forms are equal. A link that is empty
  after trimming is `invalid`; one over `maxUrlLength` UTF-8 bytes is `too_large`; one with a space
  or a control character is `invalid`; one that does not start with a scheme (a letter, then
  letters, digits, plus, dot or dash, then a colon) is `invalid`. Parsing, host case and percent
  encoding are the embedding application's, not this contract's.
- **Attachments**: the name is trimmed, non-empty, at most 255 UTF-8 bytes, not `.` or `..`, and
  has no slash, backslash or control character; the media type is trimmed, defaults to
  `application/octet-stream`, and is at most 255 bytes without a NUL. Content is data or a
  caller-owned absolute path, not both (`invalid`); data's size is its length and a declared size
  must agree or be zero; a size is not negative; a path starts with `/`. An attachment is a
  duplicate of an earlier one when its lowercased name, media type, size and content (a digest, or
  the path) are equal, and then it is dropped; two different attachments with one lowercased name
  are `invalid`. Attachment bytes (a path counts its declared size) over `maxBytes` is `too_large`.

An attachment in a case has `text` or `zeros` for data, or a `path`. The payload lists links and
attachments in first-seen order.

Not covered: text that is not valid Unicode (JSON cannot carry it), and the native loaders that
read an Android intent or an `NSExtensionItem`.
