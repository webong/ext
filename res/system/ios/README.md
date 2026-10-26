# Apple system library

`ExtSystem` observes display geometry, visible windows, native UI structure, and
permission state. On macOS it uses Core Graphics and the Accessibility API to
observe the local desktop and focused application. On iOS it reports only the
screens, windows, and views visible to the calling application. Accessibility
text is opt-in.

The same target also provides bounded PCM playback on Apple platforms. On iOS,
it exposes ReplayKit app capture and a Broadcast Upload Extension base class;
the application supplies the sink and owns the capture consent flow. On macOS
it provides ScreenCaptureKit display capture, microphone and system-audio
capture, window screenshots, atomic pointer/keyboard input against a
revalidated opaque `WindowInfo`, Accessibility actions against revalidated
elements, and typed Apple Event dispatch. The caller chooses targets and event
mappings and owns grants, permission prompting, encoding, transport and policy.
`ExtSystem` does not open settings, present approval UI, or provide remote
access.
