# Apple system observation

`ExtSystem` observes display geometry, visible windows, native UI structure, and
permission state. On macOS it uses Core Graphics and the Accessibility API to
observe the local desktop and focused application. On iOS it reports only the
screens, windows, and views visible to the calling application. Accessibility
text is opt-in. It does not capture pixels, expose native handles, request
permission, open settings, synthesize input, or provide remote access.
