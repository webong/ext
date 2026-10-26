# Apple system observation

`ExtSystem` observes display geometry and visible windows. On macOS it uses
Core Graphics to enumerate the local desktop. On iOS it reports the displays
and windows visible to the calling application. It does not read titles,
capture pixels, expose native window identifiers, request permission, or
synthesize input.
