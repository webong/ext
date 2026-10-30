# Cymonkey ownership handoff

`pkg/ext-web-js` is a temporary staging copy. Ext does not publish it. Cymonkey
will own the browser-extension peer, in-browser management, and theme code after
handoff. This package has no dependency on adapter management or the plugin
engine.

## Transfer

1. Move package source, type declarations, tests, examples, and README to a
   Cymonkey path chosen by its maintainers. Do not move generated `dist/` output.
2. Choose the package name, version, publication metadata, and release process
   there. The staging package intentionally remains `private: true`.
3. Run JavaScript tests, TypeScript checks, and example builds in the new
   location. Repeat the Firefox and Chromium host/guest smoke commands, and
   perform the separate theme browser check before claiming theme support.
4. Move or rewrite the relevant docs, then remove this staging package and its
   ext-specific references.

## Boundaries

- `res/web/extension` remains in ext for artifact inspection and packaging.
- Browser adapters remain in ext for external installation, profile discovery,
  signing, and activation.
- Cymonkey owns JavaScript embedded in an extension and any product policy built
  on it. In-browser `management` and live `theme` calls are part of that code.
- A static theme manifest is an artifact. Building one does not install, sign,
  or publish it.
