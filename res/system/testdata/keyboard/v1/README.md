# keyboard v1

`cases.json` covers the bounded text operations of a keyboard extension's editor. Only Swift and
Java have an editor; Go has none.

- **A character is a Unicode code point**: not a UTF-16 unit, and not a grapheme cluster, so a base
  letter and its combining mark are two.
- **Limits**: a selection limit of 1 to 20,000 and an insert limit of 1 to 10,000, else `invalid`.
- **Selection**: the trimmed text (strip leading and trailing U+0009 to U+000D and U+0020), or `null`
  when there is none, it is blank, or it is longer than the selection limit.
- **Insert**: empty text or a NUL is `invalid`; more than the insert limit is `too_large`. Text is
  not trimmed.
- **Delete**: a negative count is `invalid`; a count above the insert limit is `too_large`.
- **Context**: the last `limit` code points of the text before the cursor, or the first `limit` of
  the text after it; the whole text when it is not longer, and `null` for no text or a limit below 1.
  The window may cut between a base and a combining mark.

`submit`, next-keyboard switching and the native text proxies are each platform's own.
